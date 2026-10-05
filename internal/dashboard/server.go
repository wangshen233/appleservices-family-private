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
}
type credentials struct {
	AppleID  string `json:"apple_id"`
	Password string `json:"password"`
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
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	return s, nil
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
func (s *Server) readStatus() Status {
	var status Status
	data, _ := os.ReadFile(filepath.Join(s.config.Dir, "runtime.json"))
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
	return path != "" && (strings.EqualFold(filepath.Clean(path), filepath.Clean(s.config.Worker)) || strings.EqualFold(filepath.Clean(path), filepath.Join(filepath.Dir(s.config.Worker), "trusted-device.exe")))
}
func (s *Server) record() (control.Record, error) {
	var record control.Record
	data, err := os.ReadFile(filepath.Join(s.config.Dir, "control.json"))
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
	if key, err := hex.DecodeString(record.Token); err != nil || len(key) != 32 || record.PID != s.readStatus().PID || !s.owns(record.PID) {
		return control.Record{}, errors.New("后台尚未就绪")
	}
	return record, nil
}
func (s *Server) call(ctx context.Context, path string, body any, result any) error {
	record, err := s.record()
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
func (s *Server) snapshot(ctx context.Context) view {
	result := view{Status: s.readStatus(), Devices: []control.Device{}}
	s.mu.Lock()
	result.Launching, result.Message = s.launching, s.message
	s.mu.Unlock()
	var session struct {
		AppleID string `json:"appleId"`
	}
	data, _ := os.ReadFile(filepath.Join(s.config.Dir, "session.json"))
	json.Unmarshal(data, &session)
	if session.AppleID != "" {
		pieces := strings.SplitN(session.AppleID, "@", 2)
		if len(pieces) == 2 && len(pieces[0]) > 0 {
			result.Account = pieces[0][:1] + "***@" + pieces[1]
		}
	}
	if result.Status.Running {
		var live control.View
		if s.call(ctx, "/view", nil, &live) == nil && json.Unmarshal(live.Status, &result.Status) == nil {
			result.Devices = live.Devices
			result.Pending = live.Pending
			result.CodeReady = live.CodeReady
			result.ControlReady = true
		}
	}
	return result
}
func (s *Server) Start(ctx context.Context, creds credentials) control.Result {
	if !s.launchMu.TryLock() {
		return control.Result{Message: "正在启动，请稍等"}
	}
	defer s.launchMu.Unlock()
	current := s.readStatus()
	if current.Running {
		if _, err := s.record(); err == nil {
			return control.Result{OK: true, Message: "已连接到现有后台"}
		}
	}
	if strings.TrimSpace(creds.AppleID) == "" || creds.Password == "" {
		return control.Result{Message: "请输入 Apple 账户和密码"}
	}
	s.mu.Lock()
	s.launching = true
	s.message = ""
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.launching = false; s.mu.Unlock() }()
	if current.Running {
		if err := s.stop(ctx); err != nil {
			return control.Result{Message: err.Error()}
		}
	}
	if _, err := os.Stat(s.config.Worker); err != nil {
		return control.Result{Message: "缺少后台程序，请重新运行启动脚本"}
	}
	if err := os.MkdirAll(s.config.Dir, 0700); err != nil {
		return control.Result{Message: "无法写入本机状态目录"}
	}
	output, err := os.OpenFile(filepath.Join(s.config.Dir, "daemon.out.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return control.Result{Message: "无法创建运行日志"}
	}
	defer output.Close()
	diagnostic, err := os.OpenFile(filepath.Join(s.config.Dir, "daemon.err.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return control.Result{Message: "无法创建运行日志"}
	}
	defer diagnostic.Close()
	command := exec.Command(s.config.Worker, "-native-adi", "-background", "-control-api", "-gui-auth", "-dir", s.config.Dir, "-adi-dll", s.config.Library, "-python", s.config.Python)
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
			s.mu.Lock()
			s.message = "后台登录未完成，请检查账号或重新登录"
			s.mu.Unlock()
		}
	}()
	return control.Result{OK: true, Message: "正在连接 Apple，请稍等"}
}
func (s *Server) Bootstrap(ctx context.Context, appleID, password string) {
	if appleID != "" && password != "" {
		go func() {
			result := s.Start(ctx, credentials{appleID, password})
			if !result.OK {
				s.mu.Lock()
				s.message = result.Message
				s.mu.Unlock()
			}
		}()
	}
}
func (s *Server) stop(ctx context.Context) error {
	status := s.readStatus()
	if !status.Running {
		return nil
	}
	data, _ := json.Marshal(map[string]int{"pid": status.PID})
	if os.WriteFile(filepath.Join(s.config.Dir, "stop-request.json"), data, 0600) != nil {
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
			json.NewEncoder(w).Encode(s.snapshot(ctx))
			return
		case r.Method == "POST" && r.URL.Path == "/api/start":
			var creds credentials
			if !readJSON(w, r, &creds) {
				return
			}
			json.NewEncoder(w).Encode(s.Start(r.Context(), creds))
			return
		case r.Method == "POST" && r.URL.Path == "/api/stop":
			var body struct{}
			if !readJSON(w, r, &body) {
				return
			}
			result := control.Result{OK: true, Message: "后台已停止，登录状态已保留"}
			if err := s.stop(r.Context()); err != nil {
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
			var result control.Result
			if err := s.call(r.Context(), "/action", action, &result); err != nil {
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
