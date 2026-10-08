package dashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
)

//go:embed web/*
var assets embed.FS

type Config struct {
	Dir, Worker, Library, Python string
	Port                         int
}
type Server struct {
	config    Config
	listener  net.Listener
	http      *http.Server
	token     string
	launchMu  sync.Mutex
	mu        sync.Mutex
	launching bool
	message   string
	// accounts holds every registered account. The legacy single-account
	// fields below address the account whose directory is Config.Dir, so the
	// original one-account behaviour and its tests keep working unchanged.
	accounts   []*Account
	accountsMu sync.Mutex
	// registryNote reports why the account list could not be loaded, so a
	// failure is surfaced instead of silently presenting an empty list.
	registryNote string
}
type Status struct {
	PID                  int       `json:"pid"`
	Running              bool      `json:"running"`
	PushConnected        bool      `json:"push_connected"`
	Authenticated        bool      `json:"authenticated"`
	VerificationRequired bool      `json:"verification_required"`
	State                string    `json:"state"`
	UpdatedAt            time.Time `json:"updated_at"`
	LastRefreshAt        time.Time `json:"last_refresh_at"`
	LastDeviceQueryAt    time.Time `json:"last_device_query_at"`
	DeviceCount          int       `json:"device_count"`
	InstallationVisible  bool      `json:"installation_visible"`
	RefreshCount         int       `json:"refresh_count"`
	PairingUnsupported   bool      `json:"pairing_unsupported"`
	PairingState         string    `json:"pairing_state,omitempty"`
	PushReconnectCount   int       `json:"push_reconnect_count"`
	PushIssue            string    `json:"push_issue,omitempty"`
	FailureStage         string    `json:"failure_stage,omitempty"`
}
type view struct {
	Status       Status           `json:"status"`
	Devices      []control.Device `json:"devices"`
	Pending      *control.Pending `json:"pending"`
	CodeReady    bool             `json:"code_ready"`
	ControlReady bool             `json:"control_ready"`
	Launching    bool             `json:"launching"`
	Message      string           `json:"message"`
	Account      string           `json:"account"`
	LoginAccount string           `json:"login_account,omitempty"`
	Accounts     []AccountView    `json:"accounts,omitempty"`
	Selected     string           `json:"selected,omitempty"`
}
type credentials struct {
	AppleID   string `json:"apple_id"`
	Password  string `json:"password"`
	TwoFactor string `json:"twofactor,omitempty"`
}

func New(config Config) (*Server, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", intString(config.Port)))
	if err != nil {
		return nil, errors.New("面板端口已被使用，请打开已运行的面板")
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		listener.Close()
		return nil, err
	}
	s := &Server{config: config, listener: listener, token: hex.EncodeToString(key)}
	if entries, err := loadRegistry(config.Dir, config.Dir); err != nil {
		// Surfaced rather than swallowed: an unreadable registry previously
		// made the panel look like it had no accounts at all.
		s.registryNote = err.Error()
	} else {
		for _, entry := range entries {
			s.accounts = append(s.accounts, &Account{ID: entry.ID, Name: entry.Name, AppleID: entry.AppleID, Dir: entry.Dir, CreatedAt: entry.CreatedAt})
		}
	}
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	return s, nil
}

// lookup resolves one account by id. An empty id selects the account whose
// directory is the server's own, falling back to the first registered account
// (a migrated install no longer has any account living in the root directory).
func (s *Server) lookup(id string) *Account {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	if id == "" {
		for _, account := range s.accounts {
			if filepath.Clean(account.Dir) == filepath.Clean(s.config.Dir) {
				return account
			}
		}
		if len(s.accounts) > 0 {
			return s.accounts[0]
		}
		return nil
	}
	for _, account := range s.accounts {
		if account.ID == id {
			return account
		}
	}
	return nil
}

// listAccounts returns every registered account with its live status.
func (s *Server) listAccounts(ctx context.Context) []AccountView {
	s.accountsMu.Lock()
	accounts := append([]*Account(nil), s.accounts...)
	s.accountsMu.Unlock()
	out := make([]AccountView, 0, len(accounts))
	for _, account := range accounts {
		status := s.accountStatus(account)
		item := AccountView{ID: account.ID, Name: account.Name, Account: maskAccount(account.AppleID),
			State: status.State, Running: status.Running, Authenticated: status.Authenticated}
		account.mu.Lock()
		if account.message != "" {
			item.Message = account.message
		}
		account.mu.Unlock()
		out = append(out, item)
	}
	return out
}

// addAccount registers a new account in its own directory. It never reuses or
// overwrites an existing account's directory or credentials.
func (s *Server) addAccount(ctx context.Context, creds credentials) (control.Result, string) {
	if strings.TrimSpace(creds.AppleID) == "" || creds.Password == "" {
		return control.Result{Message: "请输入 Apple 账户和密码"}, ""
	}
	s.accountsMu.Lock()
	requested := strings.TrimSpace(creds.AppleID)
	requestedName := strings.SplitN(requested, "@", 2)[0]
	for _, account := range s.accounts {
		sameID := account.AppleID != "" && strings.EqualFold(account.AppleID, requested)
		// A migrated account may carry only a display name, so match on that
		// too rather than registering the same login twice under a new slug.
		sameName := account.AppleID == "" && account.Name != "" && strings.EqualFold(account.Name, requestedName)
		if sameID || sameName {
			id := account.ID
			s.accountsMu.Unlock()
			return control.Result{Message: "该账号已在列表中，请直接选择它"}, id
		}
	}
	entries := make([]*registryEntry, 0, len(s.accounts)+1)
	for _, account := range s.accounts {
		entries = append(entries, &registryEntry{ID: account.ID, Name: account.Name, AppleID: account.AppleID, Dir: account.Dir, CreatedAt: account.CreatedAt})
	}
	id, dir, err := newAccountDir(s.config.Dir, entries)
	if err != nil {
		s.accountsMu.Unlock()
		return control.Result{Message: err.Error()}, ""
	}
	// Each account gets isolated identity and provisioning state. An
	// environment-mismatch response alone does not prove permanent damage.
	if err := provisionIdentity(dir); err != nil {
		os.RemoveAll(dir)
		s.accountsMu.Unlock()
		return control.Result{Message: err.Error()}, ""
	}
	entry := &registryEntry{ID: id, Name: strings.SplitN(creds.AppleID, "@", 2)[0], AppleID: strings.TrimSpace(creds.AppleID), Dir: dir}
	entries = append(entries, entry)
	if err := saveRegistry(s.config.Dir, entries); err != nil {
		s.accountsMu.Unlock()
		return control.Result{Message: "无法保存账号列表"}, ""
	}
	account := &Account{ID: entry.ID, Name: entry.Name, AppleID: entry.AppleID, Dir: entry.Dir}
	s.accounts = append(s.accounts, account)
	s.accountsMu.Unlock()
	result := s.startAccount(ctx, account, creds)
	return result, id
}

// removeAccount stops one account and unregisters it. Its directory is kept on
// disk so its device identity is not destroyed by an accidental click.
func (s *Server) removeAccount(ctx context.Context, id string) control.Result {
	s.accountsMu.Lock()
	var target *Account
	remaining := make([]*registryEntry, 0, len(s.accounts))
	for _, account := range s.accounts {
		if account.ID == id {
			target = account
			continue
		}
		remaining = append(remaining, &registryEntry{ID: account.ID, Name: account.Name, AppleID: account.AppleID, Dir: account.Dir, CreatedAt: account.CreatedAt})
	}
	s.accountsMu.Unlock()
	if target == nil {
		return control.Result{Message: "账号不存在"}
	}
	if err := s.stopIn(ctx, target.Dir); err != nil {
		return control.Result{Message: "请先停止该账号的后台"}
	}
	if err := saveRegistry(s.config.Dir, remaining); err != nil {
		return control.Result{Message: "无法保存账号列表"}
	}
	s.accountsMu.Lock()
	s.accounts = nil
	for _, entry := range remaining {
		s.accounts = append(s.accounts, &Account{ID: entry.ID, Name: entry.Name, AppleID: entry.AppleID, Dir: entry.Dir, CreatedAt: entry.CreatedAt})
	}
	s.accountsMu.Unlock()
	return control.Result{OK: true, Message: "已从列表移除，设备身份仍保留在 accounts/" + target.ID}
}

func maskAccount(appleID string) string {
	pieces := strings.SplitN(appleID, "@", 2)
	if len(pieces) != 2 || pieces[0] == "" {
		return appleID
	}
	return pieces[0][:1] + "***@" + pieces[1]
}

func (s *Server) URL() string { return "http://" + s.listener.Addr().String() }
func (s *Server) Serve(ctx context.Context) error {
	go func() { <-ctx.Done(); s.http.Close() }()
	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// accountStatus reads one account's worker state. Each account has its own
// runtime.json in its own directory, so one account's worker can never be
// mistaken for another's.
func (s *Server) accountStatus(account *Account) Status {
	var status Status
	if account == nil {
		return status
	}
	return s.readStatusFrom(account.Dir)
}

// readStatus reads the server's own state directory.
func (s *Server) readStatus() Status { return s.readStatusFrom(s.config.Dir) }

func (s *Server) readStatusFrom(dir string) Status {
	var status Status
	data, _ := os.ReadFile(filepath.Join(dir, "runtime.json"))
	json.Unmarshal(data, &status)
	if !status.Running || !s.owns(status.PID) {
		status.Running = false
		status.Authenticated = false
		status.PushConnected = false
		if status.State == "online" {
			status.State = "stopped"
		}
	}
	return status
}
func (s *Server) owns(pid int) bool {
	path := processPath(pid)
	// Missing process information is not evidence that a saved PID is alive.
	// For an inaccessible worker, require a live authenticated control reply.
	if path == "" {
		return s.controlAlive(pid)
	}
	cleanPath := filepath.Clean(path)
	// Windows may retain the previous image under a temporary '~' suffix when
	// the worker binary is rebuilt while that worker is still running.
	cleanPath = strings.TrimRight(cleanPath, "~")
	return strings.EqualFold(cleanPath, filepath.Clean(s.config.Worker)) || strings.EqualFold(cleanPath, filepath.Join(filepath.Dir(s.config.Worker), "trusted-device.exe"))
}
func (s *Server) controlAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(filepath.Join(s.config.Dir, "control.json"))
	if err != nil {
		return false
	}
	var record control.Record
	if json.Unmarshal(data, &record) != nil || record.PID != pid {
		return false
	}
	parsed, err := url.Parse(record.URL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	key, err := hex.DecodeString(record.Token)
	if err != nil || len(key) != 32 {
		return false
	}
	request, err := http.NewRequest("GET", record.URL+"/view", nil)
	if err != nil {
		return false
	}
	request.Header.Set("X-Control-Token", record.Token)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return false
	}
	var view control.View
	if json.NewDecoder(io.LimitReader(response.Body, 128*1024)).Decode(&view) != nil {
		return false
	}
	var status struct {
		PID     int  `json:"pid"`
		Running bool `json:"running"`
	}
	return json.Unmarshal(view.Status, &status) == nil && status.PID == pid && status.Running
}
func (s *Server) record() (control.Record, error) { return s.recordFor(s.config.Dir) }

func (s *Server) recordFor(dir string) (control.Record, error) {
	var record control.Record
	data, err := os.ReadFile(filepath.Join(dir, "control.json"))
	if err != nil {
		return record, errors.New("后台尚未就绪")
	}
	if json.Unmarshal(data, &record) != nil {
		return record, errors.New("后台尚未就绪")
	}
	parsed, err := url.Parse(record.URL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return control.Record{}, errors.New("无效的本机控制地址")
	}
	if key, err := hex.DecodeString(record.Token); err != nil || len(key) != 32 || record.PID != s.readStatusFrom(dir).PID || !s.owns(record.PID) {
		return control.Record{}, errors.New("后台尚未就绪")
	}
	return record, nil
}
func (s *Server) call(ctx context.Context, path string, body any, result any) error {
	return s.callFor(ctx, s.config.Dir, path, body, result)
}
func (s *Server) callFor(ctx context.Context, dir, path string, body any, result any) error {
	record, err := s.recordFor(dir)
	if err != nil {
		return err
	}
	method := "GET"
	var reader io.Reader
	if body != nil {
		method = "POST"
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, record.URL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-Control-Token", record.Token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 95 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("无法连接后台，请检查运行状态")
	}
	defer response.Body.Close()
	client.CloseIdleConnections()
	if response.StatusCode != 200 {
		return errors.New("后台正忙，请稍后重试")
	}
	if json.NewDecoder(io.LimitReader(response.Body, 128*1024)).Decode(result) != nil {
		return errors.New("后台返回了无效数据")
	}
	return nil
}
func (s *Server) snapshot(ctx context.Context) view { return s.snapshotFor(ctx, "") }

// snapshotFor renders one account. An empty id selects the server's own
// directory, preserving the single-account behaviour.
func (s *Server) snapshotFor(ctx context.Context, id string) view {
	s.accountsMu.Lock()
	accounts := append([]*Account(nil), s.accounts...)
	s.accountsMu.Unlock()
	dir := s.config.Dir
	var account *Account
	for _, candidate := range accounts {
		if (id == "" && filepath.Clean(candidate.Dir) == filepath.Clean(s.config.Dir)) || candidate.ID == id {
			account, dir = candidate, candidate.Dir
			break
		}
	}
	// A migrated install keeps no account in the root directory, so fall back
	// to the first registered account instead of rendering an empty panel.
	if account == nil && id == "" && len(accounts) > 0 && filepath.Clean(accounts[0].Dir) != filepath.Clean(s.config.Dir) {
		account, dir = accounts[0], accounts[0].Dir
	}
	result := view{Status: s.readStatusFrom(dir), Devices: []control.Device{}}
	if account == nil {
		s.mu.Lock()
		result.Launching, result.Message = s.launching, s.message
		s.mu.Unlock()
	} else {
		account.mu.Lock()
		result.Launching, result.Message = account.launching, account.message
		account.mu.Unlock()
	}
	result.Accounts = s.listAccounts(ctx)
	result.Selected = id
	if account != nil {
		result.Selected = account.ID
		result.LoginAccount = account.AppleID
	}
	var session struct {
		AppleID string `json:"appleId"`
	}
	data, _ := os.ReadFile(filepath.Join(dir, "session.json"))
	json.Unmarshal(data, &session)
	if session.AppleID != "" {
		result.Account = maskAccount(session.AppleID)
	}
	if result.Status.Running {
		var live control.View
		if s.callFor(ctx, dir, "/view", nil, &live) == nil && json.Unmarshal(live.Status, &result.Status) == nil {
			result.Devices = live.Devices
			result.Pending = live.Pending
			result.CodeReady = live.CodeReady
			result.ControlReady = true
		}
	}
	return result
}
func (s *Server) Start(ctx context.Context, creds credentials) control.Result {
	// Start acts on the account that owns the server's own directory. A
	// registered account is started through startAccount so one account's
	// credentials can never land in another account's directory.
	if account := s.lookup(""); account != nil {
		return s.startAccount(ctx, account, creds)
	}
	return s.startIn(ctx, s.config.Dir, creds, nil)
}

// startAccount starts one registered account inside its own directory.
func (s *Server) startAccount(ctx context.Context, account *Account, creds credentials) control.Result {
	if account == nil {
		return control.Result{Message: "账号不存在"}
	}
	if creds.AppleID == "" {
		creds.AppleID = account.AppleID
	}
	if !strings.EqualFold(strings.TrimSpace(creds.AppleID), account.AppleID) {
		return control.Result{Message: "登录账号与所选工作区不一致，请使用添加账号"}
	}
	return s.startIn(ctx, account.Dir, creds, account)
}

func (s *Server) startIn(ctx context.Context, dir string, creds credentials, account *Account) control.Result {
	if !s.launchMu.TryLock() {
		return control.Result{Message: "正在启动，请稍等"}
	}
	defer s.launchMu.Unlock()
	s.setMessage(account, "")
	current := s.readStatusFrom(dir)
	if current.Running {
		if _, err := s.recordFor(dir); err == nil {
			return control.Result{OK: true, Message: "已连接到现有后台"}
		}
	}
	if strings.TrimSpace(creds.AppleID) == "" || creds.Password == "" {
		return control.Result{Message: "请输入 Apple 账户和密码"}
	}
	if creds.TwoFactor == "" {
		creds.TwoFactor = "sms"
	}
	if creds.TwoFactor != "sms" && creds.TwoFactor != "circle" && creds.TwoFactor != "trusted-device" {
		return control.Result{Message: "不支持的验证方式"}
	}
	s.setLaunching(account, true)
	defer s.setLaunching(account, false)
	if current.Running {
		if err := s.stopIn(ctx, dir); err != nil {
			return control.Result{Message: err.Error()}
		}
	}
	if _, err := os.Stat(s.config.Worker); err != nil {
		return control.Result{Message: "缺少后台程序，请重新运行启动脚本"}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return control.Result{Message: "无法写入本机状态目录"}
	}
	output, err := os.OpenFile(filepath.Join(dir, "daemon.out.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return control.Result{Message: "无法创建运行日志"}
	}
	defer output.Close()
	diagnostic, err := os.OpenFile(filepath.Join(dir, "daemon.err.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return control.Result{Message: "无法创建运行日志"}
	}
	defer diagnostic.Close()
	command := exec.Command(s.config.Worker, "-native-adi", "-background", "-control-api", "-gui-auth", "-dir", dir, "-adi-dll", s.config.Library, "-python", s.config.Python)
	command.Args = append(command.Args, "-twofactor", creds.TwoFactor)
	configureProcess(command)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "APPLESERVICES_APPLEID") && !strings.EqualFold(key, "APPLESERVICES_PASSWORD") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "APPLESERVICES_APPLEID="+strings.TrimSpace(creds.AppleID), "APPLESERVICES_PASSWORD="+creds.Password)
	command.Stdout, command.Stderr = output, diagnostic
	if command.Start() != nil {
		return control.Result{Message: "后台启动失败，请检查本机依赖"}
	}
	go func() {
		if err := command.Wait(); err != nil {
			message := "后台异常停止，请重新连接"
			if detail := lastProcessError(diagnostic.Name()); detail != "" {
				message += "：" + detail
			}
			s.setMessage(account, message)
		}
	}()
	return control.Result{OK: true, Message: "正在连接 Apple，请稍等"}
}

func lastProcessError(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// Never forward arbitrary stderr (which can contain credentials).
		switch {
		case strings.Contains(line, "a provisioned device state is required"):
			return "所选目录缺少有效设备身份，请选择已有账号，或通过添加账号建立身份"
		case strings.Contains(line, "APPLESERVICES_APPLEID and APPLESERVICES_PASSWORD are required"):
			return "请重新输入当前账号密码"
		default:
			return "请检查该账号的本机诊断日志；设备身份仍保留"
		}
	}
	return ""
}

// setLaunching and setMessage record progress on the account when there is one,
// and on the server itself for the legacy single-account path.
func (s *Server) setLaunching(account *Account, value bool) {
	if account == nil {
		s.mu.Lock()
		s.launching = value
		s.mu.Unlock()
		return
	}
	account.mu.Lock()
	account.launching = value
	account.mu.Unlock()
}

func (s *Server) setMessage(account *Account, message string) {
	if account == nil {
		s.mu.Lock()
		s.message = message
		s.mu.Unlock()
		return
	}
	account.mu.Lock()
	account.message = message
	account.mu.Unlock()
}

func (s *Server) Bootstrap(ctx context.Context, appleID, password string) {
	if appleID != "" && password != "" {
		go func() {
			result := s.Start(ctx, credentials{AppleID: appleID, Password: password})
			if !result.OK {
				s.mu.Lock()
				s.message = result.Message
				s.mu.Unlock()
			}
		}()
	}
}
func (s *Server) stop(ctx context.Context) error { return s.stopIn(ctx, s.config.Dir) }

// stopIn stops the worker that owns one account directory. Each account has its
// own worker, so stopping one leaves every other account running.
func (s *Server) stopIn(ctx context.Context, dir string) error {
	status := s.readStatusFrom(dir)
	if !status.Running {
		return nil
	}
	data, _ := json.Marshal(map[string]int{"pid": status.PID})
	if os.WriteFile(filepath.Join(dir, "stop-request.json"), data, 0600) != nil {
		return errors.New("无法停止后台")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ticker.C:
			if !s.owns(status.PID) {
				return nil
			}
		case <-timer.C:
			return errors.New("后台仍在清理连接，请稍后查看")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func readJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON required", 415)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid input", 400)
		return false
	}
	return true
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-AppleServices-UI", "1")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if r.Host != s.listener.Addr().String() {
		http.Error(w, "local host required", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		origin := r.Header.Get("Origin")
		if (origin != "" && origin != s.URL()) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-App-Token")), []byte(s.token)) != 1 {
			http.Error(w, `{"message":"请刷新面板后重试"}`, 403)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/view":
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			json.NewEncoder(w).Encode(s.snapshotFor(ctx, r.URL.Query().Get("account")))
			return
		case r.Method == "POST" && r.URL.Path == "/api/start":
			var creds credentials
			if !readJSON(w, r, &creds) {
				return
			}
			if account := r.URL.Query().Get("account"); account != "" {
				json.NewEncoder(w).Encode(s.startAccount(r.Context(), s.lookup(account), creds))
				return
			}
			json.NewEncoder(w).Encode(s.Start(r.Context(), creds))
			return
		case r.Method == "POST" && r.URL.Path == "/api/accounts/add":
			// A new account always gets its own directory, so an existing
			// account's session is never overwritten.
			var creds credentials
			if !readJSON(w, r, &creds) {
				return
			}
			result, id := s.addAccount(r.Context(), creds)
			if id != "" {
				result.Message = strings.TrimSpace(result.Message)
			}
			json.NewEncoder(w).Encode(result)
			return
		case r.Method == "POST" && r.URL.Path == "/api/accounts/remove":
			var body struct {
				ID string `json:"id"`
			}
			if !readJSON(w, r, &body) {
				return
			}
			json.NewEncoder(w).Encode(s.removeAccount(r.Context(), body.ID))
			return
		case r.Method == "POST" && r.URL.Path == "/api/stop":
			var body struct {
				Account string `json:"account"`
			}
			if !readJSON(w, r, &body) {
				return
			}
			if body.Account == "" {
				body.Account = r.URL.Query().Get("account")
			}
			dir := s.config.Dir
			if body.Account == "" {
				if selected := s.lookup(""); selected != nil {
					body.Account = selected.ID
				}
			}
			if body.Account != "" {
				account := s.lookup(body.Account)
				if account == nil {
					json.NewEncoder(w).Encode(control.Result{Message: "账号不存在"})
					return
				}
				dir = account.Dir
			}
			result := control.Result{OK: true, Message: "后台已停止，登录状态已保留"}
			if err := s.stopIn(r.Context(), dir); err != nil {
				result.OK = false
				result.Message = "后台停止未完成，请稍后查看"
			}
			json.NewEncoder(w).Encode(result)
			return
		case r.Method == "POST" && r.URL.Path == "/api/action":
			var action control.Action
			if !readJSON(w, r, &action) {
				return
			}
			dir := s.config.Dir
			if account := r.URL.Query().Get("account"); account != "" {
				found := s.lookup(account)
				if found == nil {
					json.NewEncoder(w).Encode(control.Result{Message: "账号不存在"})
					return
				}
				dir = found.Dir
			}
			var result control.Result
			if err := s.callFor(r.Context(), dir, "/action", action, &result); err != nil {
				result.Message = err.Error()
			}
			json.NewEncoder(w).Encode(result)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}
	if r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	file := ""
	switch r.URL.Path {
	case "/":
		file = "index.html"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case "/app.js":
		file = "app.js"
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case "/app.css":
		file = "app.css"
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	default:
		http.NotFound(w, r)
		return
	}
	data, err := assets.ReadFile("web/" + file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if file == "index.html" {
		data = bytes.ReplaceAll(data, []byte("__APP_TOKEN__"), []byte(s.token))
	}
	w.Write(data)
}
