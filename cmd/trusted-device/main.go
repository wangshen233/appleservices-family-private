package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/anisette"
	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/circle"
	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/nativeadi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "trusted-device:", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	dir := flag.String("dir", "./state", "existing device/session directory")
	name := flag.String("name", "appleservices", "name announced to Apple")
	certificatePath := flag.String("push-cert", "", "Apple-issued client push certificate PEM file")
	keyPath := flag.String("push-key", "", "matching client push private key PEM file")
	codeProvider := flag.String("code-provider", "", "absolute path to a working local ADI code-provider executable")
	server := flag.String("anisette", "https://ani.sidestore.io", "anisette server for this identity")
	method := flag.String("twofactor", "sms", "initial sign-in method: sms, trusted-device or circle")
	check := flag.Bool("check", false, "check local prerequisites without contacting Apple")
	native := flag.Bool("native-push", false, "use the installed iCloud for Windows APS daemon")
	probeNative := flag.Bool("probe-native-push", false, "check native token and local callback; no account registration")
	listenOnly := flag.Bool("listen-only", false, "register and observe notifications; do not approve requests or generate codes")
	listenFor := flag.Duration("listen-for", 0, "stop after this duration; 0 keeps listening")
	useNativeADI := flag.Bool("native-adi", false, "generate headers and verification codes with the verified Windows native ADI module")
	adiLibrary := flag.String("adi-dll", "./.cache/openbubbles-store/rust_lib_bluebubbles.dll", "verified OpenBubbles 1.18.800.0 native module path")
	python := flag.String("python", "python", "64-bit Python executable for the native ADI bridge")
	background := flag.Bool("background", false, "keep running with closed stdin; never approve requests or log generated codes")
	refreshEvery := flag.Duration("refresh-every", 10*time.Minute, "refresh account authentication, registration and device status at this interval")
	controlAPI := flag.Bool("control-api", false, "enable authenticated local dashboard control")
	guiAuth := flag.Bool("gui-auth", false, "complete initial SMS verification through the dashboard")
	flag.Parse()
	if *background {
		*controlAPI = true
	}
	if *useNativeADI {
		*native = true
		if *codeProvider != "" {
			return errors.New("native-adi cannot be combined with an external code-provider")
		}
	}
	if *probeNative {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		length, err := aps.ProbeNativePush(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Native APS token length: %d. Local COM callback verified.\n", length)
		fmt.Println("Account trust and remote code delivery have not been verified.")
		return nil
	}
	if *method != "sms" && *method != "trusted-device" && *method != "circle" {
		return errors.New("twofactor must be sms, trusted-device or circle")
	}
	if *listenFor < 0 {
		return errors.New("listen-for must not be negative")
	}
	if *refreshEvery < time.Minute {
		return errors.New("refresh-every must be at least one minute")
	}
	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*dir) == "" {
		return errors.New("name and state directory are required")
	}
	if !*listenOnly && !*useNativeADI {
		if err := aps.ValidateCodeProvider(*codeProvider); err != nil {
			return err
		}
	}
	store := fileStore{dir: *dir}
	device, err := store.LoadDevice()
	if err != nil || device == nil || len(device.Identifier) != 16 || len(device.ProvisioningBlob) == 0 {
		return errors.New("a provisioned device state is required; complete the existing login first")
	}
	savedPath := filepath.Join(*dir, "push.json")
	var pushState aps.State
	var push pushTransport
	if *native {
		if *certificatePath != "" || *keyPath != "" {
			return errors.New("native-push cannot be combined with push-cert or push-key")
		}
		push, err = aps.NewNativeClient()
	} else {
		if *certificatePath != "" || *keyPath != "" {
			if *certificatePath == "" || *keyPath == "" {
				return errors.New("both push-cert and push-key are required")
			}
			pushState.CertificatePEM, err = os.ReadFile(*certificatePath)
			if err != nil {
				return errors.New("could not read client push certificate")
			}
			pushState.PrivateKeyPEM, err = os.ReadFile(*keyPath)
			if err != nil {
				return errors.New("could not read client push private key")
			}
		} else {
			data, err := os.ReadFile(savedPath)
			if err != nil {
				return errors.New("client push certificate/key are unavailable; provide push-cert and push-key")
			}
			if err := json.Unmarshal(data, &pushState); err != nil {
				return errors.New("invalid saved push state")
			}
		}
		push, err = aps.NewClient(pushState)
	}
	if err != nil {
		return err
	}
	if *check {
		if *useNativeADI {
			library, err := filepath.Abs(*adiLibrary)
			if err != nil {
				return errors.New("could not resolve native ADI module path")
			}
			if err := nativeadi.ValidateLibrary(library); err != nil {
				return err
			}
			if err := nativeadi.CheckRuntime(*python); err != nil {
				return err
			}
		}
		fmt.Println("Local inputs available. Apple trust and code delivery have not been verified.")
		return nil
	}
	appleID, password := os.Getenv("APPLESERVICES_APPLEID"), os.Getenv("APPLESERVICES_PASSWORD")
	if appleID == "" || password == "" {
		return errors.New("APPLESERVICES_APPLEID and APPLESERVICES_PASSWORD are required")
	}
	os.Unsetenv("APPLESERVICES_APPLEID")
	os.Unsetenv("APPLESERVICES_PASSWORD")
	status := runtimeStatus{PID: os.Getpid(), Running: true, State: "starting"}
	if err := status.save(*dir); err != nil {
		return err
	}
	defer func() {
		status.Running, status.PushConnected = false, false
		status.Authenticated = false
		if status.State != "verification_required" {
			if runErr != nil {
				status.FailureStage, status.State = status.State, "failed"
			} else {
				status.State = "stopped"
			}
			status.VerificationRequired = false
		}
		if err := status.save(*dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	var panel *control.Server
	var panelCommands <-chan control.Command
	if *controlAPI {
		panel, err = control.New(*dir)
		if err != nil {
			return err
		}
		defer panel.Close()
		panelCommands = panel.Commands
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go watchStop(ctx, cancel, *dir, status.PID)
	if *listenFor > 0 {
		var stopListening context.CancelFunc
		ctx, stopListening = context.WithTimeout(ctx, *listenFor)
		defer stopListening()
	}
	fmt.Fprintln(os.Stderr, "Authenticating the APS connection ...")
	connectCtx, stop := context.WithTimeout(ctx, 45*time.Second)
	err = push.Connect(connectCtx)
	stop()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer func() { push.Close() }()
	status.PushConnected = true
	status.State = "initializing"
	if err := status.save(*dir); err != nil {
		return err
	}
	if !*native {
		if err := savePush(savedPath, push.State()); err != nil {
			return err
		}
	}
	events := make(chan aps.Event, 16)
	errorsCh := make(chan error, 1)
	listen := func(connection pushTransport) {
		go func() {
			errorsCh <- connection.Listen(ctx, func(event aps.Event) error {
				select {
				case events <- event:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
	}
	listen(push)
	identity := sha256.Sum256(device.Identifier)
	serial := device.SerialNumber
	if serial == "" {
		serial = "AS" + strings.ToUpper(fmt.Sprintf("%x", identity[:5]))
	}
	options := []appleservices.Option{appleservices.WithAnisetteServers(*server), appleservices.WithDeviceSerialNumber(serial), appleservices.WithPushToken(push.State().Token)}
	var localADI *nativeadi.Client
	if *useNativeADI {
		library, err := filepath.Abs(*adiLibrary)
		if err != nil {
			return errors.New("could not resolve native ADI module path")
		}
		localADI, err = nativeadi.NewClient(nativeadi.Config{LibraryPath: library, ProfileDir: filepath.Join(*dir, "native-adi"),
			Python: *python, Identifier: device.Identifier, Serial: serial, PushToken: push.State().Token})
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Starting the verified local ADI generation module ...")
		startCtx, stop := context.WithTimeout(ctx, 90*time.Second)
		err = localADI.Start(startCtx)
		stop()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		defer localADI.Close()
		options = append(options, appleservices.WithAnisette(localADI))
		fmt.Fprintln(os.Stderr, "Local six-digit generation is available for the same device identity.")
	}
	generateCode := func(codeCtx context.Context) (string, error) {
		if localADI != nil {
			return localADI.Code(codeCtx)
		}
		return aps.CodeFromProcess(codeCtx, *codeProvider, codeRequest(device))
	}
	status.State = "signing_in"
	if err := status.save(*dir); err != nil {
		return err
	}
	login, err := appleservices.BeginLogin(appleservices.Credentials{AppleID: appleID, Password: password}, store, options...)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}
	input := bufio.NewReader(os.Stdin)
	if login.NeedsTwoFactor() && *method == "circle" {
		if panel == nil || !*guiAuth {
			return errors.New("Circle login requires the dashboard control API")
		}
		if err := completeCircleLogin(ctx, login, push.State().Token, events, errorsCh, panelCommands, &status, *dir); err != nil {
			return err
		}
	}
	if login.NeedsTwoFactor() {
		if *background && (!*guiAuth || panel == nil) {
			status.VerificationRequired = true
			status.State = "verification_required"
			return errVerificationRequired
		}
		factor := appleservices.SMS
		if *method == "trusted-device" {
			factor = appleservices.TrustedDevice
		}
		if err := login.RequestCode(factor); err != nil {
			return err
		}
		if panel != nil && *guiAuth {
			status.State, status.VerificationRequired = "awaiting_sms", true
			if err := status.save(*dir); err != nil {
				return err
			}
			lastSent := time.Now()
			verified := false
			for !verified {
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					return nil
				case command := <-panelCommands:
					if command.Context.Err() != nil {
						continue
					}
					result := control.Result{}
					switch command.Action.Action {
					case "resend-sms":
						if time.Since(lastSent) < time.Minute {
							result.Message = "请间隔一分钟再重发短信"
						} else if login.RequestCode(factor) != nil {
							result.Message = "短信发送失败，请稍后重试"
						} else {
							lastSent = time.Now()
							result.OK = true
							result.Message = "短信已发送"
						}
					case "submit-sms":
						if login.SubmitCode(factor, command.Code) != nil {
							result.Message = "验证码无效或已过期，请重新输入"
						} else {
							result.OK = true
							result.Message = "验证码已验证，正在完成登录"
							verified = true
						}
					default:
						result.Message = "请先完成短信验证"
					}
					command.Reply <- result
				}
				if stopRequested(*dir, status.PID) {
					return nil
				}
			}
			status.State, status.VerificationRequired = "signing_in", false
			if err := status.save(*dir); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "Enter the initial %s verification code: ", *method)
			code, err := input.ReadString('\n')
			if err != nil {
				return errors.New("could not read initial verification code")
			}
			if err := login.SubmitCode(factor, strings.TrimSpace(code)); err != nil {
				return err
			}
		}
	}
	client, err := login.Client()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}
	var active maintainedLogin = login
	accountID := client.ADSID
	circles := newCircleManager(accountID, client.DSID)
	registration := appleservices.DeviceRegistration{Name: *name, PushToken: push.State().Token}
	if !*listenOnly {
		generationCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		_, err = generateCode(generationCtx)
		stop()
		if err != nil {
			return fmt.Errorf("code-generation prerequisite failed before account registration: %w", err)
		}
	}
	if err := active.RegisterDevice(registration); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	fmt.Println("Apple accepted the registration request. Waiting for real AuthKit delivery; trusted-device reception is not yet verified.")
	devices, err := active.AccountDevices()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not verify account device status:", err)
	} else {
		status.setDevices(devices, *name)
		if panel != nil {
			panel.SetDevices(panelDevices(devices, *name))
		}
		for _, entry := range devices {
			if entry.Name == *name {
				fmt.Println("An entry with the announced name is visible in the account device list; this alone does not prove trust.")
				if entry.IsTrustedDevice != nil {
					fmt.Printf("Account-reported trusted status: %t.\n", *entry.IsTrustedDevice)
				}
			}
		}
	}
	if err := active.SaveSession(); err != nil {
		return err
	}
	status.Authenticated, status.State = true, "online"
	if err != nil {
		status.State = "device_query_failed"
	}
	status.LastRefreshAt = time.Now().UTC()
	if panel != nil {
		panel.SetReady(!*listenOnly)
	}
	if err := status.save(*dir); err != nil {
		return err
	}
	fmt.Printf("Session remains active; account renewal runs every %s.\n", *refreshEvery)
	if *background {
		fmt.Println("Background mode: closed stdin does not stop reception; requests are never approved automatically.")
	}
	if *listenOnly {
		fmt.Println("Observation mode: no codes will be generated and no sign-in requests will be approved.")
	} else if !*background {
		fmt.Println("Type code to generate a verification code, devices to query device status, or wait for a sign-in request.")
	}
	var pendingMu sync.Mutex
	var pending string
	var pendingKind string
	sendCircle := func(event aps.Event, response circle.Response) error {
		sender, ok := active.(interface {
			RespondToCircle(appleservices.CircleReply) error
		})
		if !ok {
			return errors.New("Circle sender unavailable")
		}
		return sender.RespondToCircle(appleservices.CircleReply{Transaction: event.CircleTransactionID, IDMSData: event.IDMSData, Step: response.Step, PAKE: response.PAKE, ErrorCode: response.ErrorCode, PushToken: registration.PushToken})
	}
	freshCircleCode := func() (string, error) {
		generationCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		return generateCode(generationCtx)
	}
	var reconnectTimer *time.Timer
	var reconnectTick <-chan time.Time
	reconnected := make(chan pushConnection)
	reconnectDelay := 2 * time.Second
	defer func() {
		if reconnectTimer != nil {
			reconnectTimer.Stop()
		}
	}()
	scheduleReconnect := func() {
		reconnectTimer = time.NewTimer(reconnectDelay)
		reconnectTick = reconnectTimer.C
		reconnectDelay = nextPushDelay(reconnectDelay)
	}
	var choices <-chan string
	if !*listenOnly && !*background {
		inputChoices := make(chan string)
		choices = inputChoices
		go func() {
			defer close(inputChoices)
			for {
				line, err := input.ReadString('\n')
				if err != nil {
					return
				}
				select {
				case inputChoices <- strings.TrimSpace(line):
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	refresh := time.NewTicker(*refreshEvery)
	defer refresh.Stop()
	beginRenewal := func() (maintainedLogin, error) {
		return appleservices.BeginLogin(appleservices.Credentials{AppleID: appleID, Password: password}, store, options...)
	}
	renew := func() control.Result {
		renewed, devices, err := renewSession(beginRenewal, accountID, registration)
		if err != nil {
			status.State = "refresh_failed"
			if errors.Is(err, errVerificationRequired) {
				status.State, status.VerificationRequired = "verification_required", true
			}
			status.save(*dir)
			return control.Result{Message: "续登录失败，请检查登录状态"}
		}
		active = renewed
		status.State, status.Authenticated = "online", true
		status.LastRefreshAt = time.Now().UTC()
		status.RefreshCount++
		status.setDevices(devices, *name)
		if panel != nil {
			panel.SetDevices(panelDevices(devices, *name))
		}
		if err := status.save(*dir); err != nil {
			return control.Result{Message: "无法保存登录状态"}
		}
		return control.Result{OK: true, Message: "登录已续期，设备列表已更新"}
	}
	performAction := func(actionCtx context.Context, action, visibleRequestID, commandCode string) control.Result {
		if action == "storage-preview" {
			previewer, ok := active.(interface {
				PreviewStoragePurchase(context.Context) (*icloud.StoreFormInfo, error)
			})
			if !ok {
				return control.Result{Message: "后台版本不支持购买表单检查", Stage: "worker"}
			}
			queryCtx, done := context.WithTimeout(actionCtx, 75*time.Second)
			defer done()
			info, err := previewer.PreviewStoragePurchase(queryCtx)
			if err != nil {
				result := control.Result{Message: "表单检查未完成，未提交购买", Stage: "purchase-preview"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
				}
				return result
			}
			return control.Result{OK: true, Message: "已读取 Apple 购买表单，未提交购买", StoreForm: info}
		}
		if action == "storage-authenticate" {
			checker, ok := active.(interface {
				CheckStoragePurchaseSession(context.Context, string) (*icloud.StoreAuthenticationInfo, error)
			})
			if !ok {
				return control.Result{Message: "后台版本不支持购买会话验证", Stage: "worker"}
			}
			queryCtx, done := context.WithTimeout(actionCtx, 75*time.Second)
			defer done()
			info, err := checker.CheckStoragePurchaseSession(queryCtx, commandCode)
			if err != nil {
				result := control.Result{Message: "购买会话认证未完成，iCloud 登录会话已保留", Stage: "purchase-store-authentication"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("购买会话认证失败：%s，HTTP %d", failure.Stage, failure.HTTPStatus)
					if failure.Stage == "purchase-store-2fa" {
						result.Message = "Apple Store 需要独立的六位验证码，iCloud 登录仍保留"
					}
				}
				return result
			}
			return control.Result{OK: true, Message: "Apple Store 认证已通过，购买会话已保留；未提交购买", StoreAuthentication: info}
		}
		if action == "storage-info" {
			reader, ok := active.(interface {
				StorageInfo(context.Context) (*appleservices.StorageInfo, error)
			})
			if !ok {
				return control.Result{Message: "后台版本不支持储存查询，请更新后台", Stage: "worker"}
			}
			queryCtx, done := context.WithTimeout(ctx, 30*time.Second)
			defer done()
			info, err := reader.StorageInfo(queryCtx)
			if err != nil {
				result := control.Result{Message: "储存查询未完成，登录会话已保留", Stage: "storage"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("储存查询失败：%s，HTTP %d。登录会话已保留", failure.Stage, failure.HTTPStatus)
				}
				return result
			}
			return control.Result{OK: true, Storage: info, Message: "Apple 已返回当前储存配额"}
		}
		if action == "storage-plan" {
			reader, ok := active.(interface {
				StoragePlanInfo(context.Context) (*appleservices.StoragePlanInfo, error)
			})
			if !ok {
				return control.Result{Message: "后台版本不支持方案查询，请更新后台", Stage: "worker"}
			}
			queryCtx, done := context.WithTimeout(ctx, 30*time.Second)
			defer done()
			plans, err := reader.StoragePlanInfo(queryCtx)
			if err != nil {
				result := control.Result{Message: "可购方案查询未完成，登录会话已保留", Stage: "storage-plan"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("可购方案查询失败：%s，HTTP %d。登录会话已保留", failure.Stage, failure.HTTPStatus)
				}
				return result
			}
			return control.Result{OK: true, StoragePlan: plans, Message: "Apple 已返回可购储存方案"}
		}
		if action == "storage-probe" {
			queryCtx, done := context.WithTimeout(actionCtx, 90*time.Second)
			defer done()
			var probes []icloud.StorageEndpointProbe
			var err error
			// A local probe-urls.json (diagnostic only) replaces the built-in
			// endpoint list so probes can iterate without a worker restart.
			if specs := loadProbeSpecs(*dir); specs != nil {
				custom, ok := active.(interface {
					ProbeCustom(context.Context, []icloud.ProbeSpec, bool) ([]icloud.StorageEndpointProbe, error)
				})
				if !ok {
					return control.Result{Message: "后台版本不支持自定义探测", Stage: "worker"}
				}
				probes, err = custom.ProbeCustom(queryCtx, specs, commandCode == "store")
			} else {
				prober, ok := active.(interface {
					ProbeStorageEndpoints(context.Context, bool) ([]icloud.StorageEndpointProbe, error)
				})
				if !ok {
					return control.Result{Message: "后台版本不支持储存端点探测", Stage: "worker"}
				}
				probes, err = prober.ProbeStorageEndpoints(queryCtx, commandCode == "store")
			}
			if err != nil {
				result := control.Result{Message: "端点探测未完成", Stage: "storage-probe"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("端点探测未完成：%s，HTTP %d", failure.Stage, failure.HTTPStatus)
				}
				return result
			}
			return control.Result{OK: true, Probes: probes, Message: "只读探测完成（无变更）"}
		}
		if action == "storage-buy" {
			checker, ok := active.(interface {
				CheckStoragePurchaseSession(context.Context, string) (*icloud.StoreAuthenticationInfo, error)
			})
			if !ok {
				return control.Result{Message: "后台版本不支持商店直购", Stage: "worker"}
			}
			buyer, ok := active.(interface {
				SubmitStorageBuyDirect(context.Context) (*appleservices.PurchaseResult, error)
			})
			if !ok {
				return control.Result{Message: "后台版本不支持商店直购", Stage: "worker"}
			}
			queryCtx, done := context.WithTimeout(actionCtx, 90*time.Second)
			defer done()
			if _, err := checker.CheckStoragePurchaseSession(queryCtx, commandCode); err != nil {
				result := control.Result{Message: "商店会话未建立，未提交购买", Stage: "purchase-store-authentication"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("商店会话失败：%s，HTTP %d", failure.Stage, failure.HTTPStatus)
					if failure.Stage == "purchase-store-2fa" {
						result.Message = "Apple Store 需要六位验证码"
					}
				}
				return result
			}
			purchase, err := buyer.SubmitStorageBuyDirect(queryCtx)
			if err != nil {
				result := control.Result{Message: "购买请求未完成", Stage: "purchase-buy"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("购买请求失败：%s，HTTP %d", failure.Stage, failure.HTTPStatus)
				}
				return result
			}
			if purchase == nil {
				return control.Result{Message: "Apple 未返回购买状态", Stage: "purchase-buy"}
			}
			if purchase.State == "completed" {
				return control.Result{OK: true, Purchase: purchase, Message: "Apple 已确认购买", Stage: "purchase-completed"}
			}
			return control.Result{OK: true, Purchase: purchase, Message: "Apple 返回后续步骤，订阅尚未完成", Stage: "purchase-handoff"}
		}
		if action == "storage-purchase" {
			queryCtx, done := context.WithTimeout(actionCtx, 75*time.Second)
			defer done()
			target := appleservices.PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}
			// Default: the web-parity chain the official client runs (no
			// native Store session). The "store-session" code selects the
			// earlier SAP-login-first variant.
			var purchase *appleservices.PurchaseResult
			var err error
			if commandCode == "store-session" {
				buyer, ok := active.(interface {
					SubmitStoragePurchaseWithStoreSession(context.Context, appleservices.PurchaseTarget) (*appleservices.PurchaseResult, error)
				})
				if !ok {
					return control.Result{Message: "后台版本不支持 Store 会话购买变体", Stage: "worker"}
				}
				purchase, err = buyer.SubmitStoragePurchaseWithStoreSession(queryCtx, target)
			} else {
				buyer, ok := active.(interface {
					SubmitStoragePurchase(context.Context, appleservices.PurchaseTarget) (*appleservices.PurchaseResult, error)
				})
				if !ok {
					return control.Result{Message: "后台版本不支持 iCloud+ 购买路由，请更新后台", Stage: "worker"}
				}
				purchase, err = buyer.SubmitStoragePurchase(queryCtx, target)
			}
			if err != nil {
				result := control.Result{Message: "购买请求未完成，登录会话已保留", Stage: "purchase"}
				var failure *icloud.StorageError
				if errors.As(err, &failure) {
					result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
					result.Message = fmt.Sprintf("购买请求失败：%s，HTTP %d。登录会话已保留", failure.Stage, failure.HTTPStatus)
				}
				return result
			}
			if purchase == nil {
				return control.Result{Message: "Apple 未返回购买状态", Stage: "purchase"}
			}
			if purchase.State == "completed" {
				return control.Result{OK: true, Purchase: purchase, Message: "iCloud+ 50GB 订阅已完成，付费容量已核对", Stage: "purchase-completed"}
			}
			if purchase.State == "existing-paid-plan" {
				return control.Result{OK: true, Purchase: purchase, Message: "账号已存在至少 50GB 的付费容量，未重复提交订阅", Stage: "purchase-existing"}
			}
			if purchase.State == "verification-pending" {
				return control.Result{OK: true, Purchase: purchase, Message: "Apple 返回完成页面，但付费容量尚未核对，请先读取容量", Stage: "purchase-verification-pending"}
			}
			return control.Result{OK: true, Purchase: purchase, Message: "Apple 返回后续认证或确认步骤，订阅尚未完成", Stage: "purchase-handoff"}
		}
		if action == "refresh" {
			return renew()
		}
		if action == "validate-account" {
			if err := active.BeginAccountValidation(); err != nil {
				status.State, status.VerificationRequired = "refresh_failed", true
				_ = status.save(*dir)
				return control.Result{Message: "账户验证启动失败，请稍后重试"}
			}
			if active.NeedsTwoFactor() {
				status.State, status.Authenticated, status.VerificationRequired = "awaiting_account_validation", false, true
				if err := status.save(*dir); err != nil {
					return control.Result{Message: "无法保存账户验证状态"}
				}
				return control.Result{OK: true, Message: "验证码已发送到受信任设备"}
			}
			client, err := active.Client()
			if err != nil || client == nil || client.ADSID != accountID {
				status.State, status.VerificationRequired = "refresh_failed", true
				_ = status.save(*dir)
				return control.Result{Message: "账户验证未完成，请稍后重试"}
			}
			if err := active.SaveSession(); err != nil {
				return control.Result{Message: "账户验证已完成，但登录状态保存失败"}
			}
			status.State, status.Authenticated, status.VerificationRequired = "online", true, false
			status.LastRefreshAt = time.Now().UTC()
			_ = status.save(*dir)
			return control.Result{OK: true, Message: "Apple 账户验证已完成"}
		}
		if action == "submit-account-code" {
			if status.State != "awaiting_account_validation" {
				return control.Result{Message: "当前没有待完成的账户验证"}
			}
			if err := active.SubmitCode(appleservices.TrustedDevice, commandCode); err != nil {
				return control.Result{Message: "验证码无效或已过期，请重新输入"}
			}
			client, err := active.Client()
			if err != nil || client == nil || client.ADSID != accountID {
				return control.Result{Message: "验证码已通过，但登录会话未建立"}
			}
			if err := active.SaveSession(); err != nil {
				return control.Result{Message: "验证已完成，但登录状态保存失败"}
			}
			status.State, status.Authenticated, status.VerificationRequired = "online", true, false
			status.LastRefreshAt = time.Now().UTC()
			if err := status.save(*dir); err != nil {
				return control.Result{Message: "验证已完成，但状态保存失败"}
			}
			return control.Result{OK: true, Message: "Apple 账户验证已完成"}
		}
		if action == "devices" {
			devices, err := active.AccountDevices()
			if err != nil {
				return control.Result{Message: "设备列表获取失败，请稍后重试"}
			}
			status.setDevices(devices, *name)
			if panel != nil {
				panel.SetDevices(panelDevices(devices, *name))
			}
			if active.SaveSession() != nil || status.save(*dir) != nil {
				return control.Result{Message: "无法保存登录状态"}
			}
			return control.Result{OK: true, Message: fmt.Sprintf("已更新 %d 台设备", len(devices))}
		}
		if action != "code" && action != "allow" && action != "deny" {
			return control.Result{Message: "当前无需短信验证码"}
		}
		if *listenOnly {
			return control.Result{Message: "观察模式不能生成验证码或批准登录"}
		}
		pendingMu.Lock()
		transaction := pending
		kind := pendingKind
		pendingMu.Unlock()
		if action != "code" && (transaction == "" || (visibleRequestID != "" && visibleRequestID != requestID(transaction))) {
			return control.Result{Message: "登录请求已取消或发生变化"}
		}
		code := ""
		if action == "allow" && kind == "circle" {
			code, err := circles.approve(transaction, sendCircle)
			if err != nil {
				return control.Result{Message: "配对响应未完成，请重试当前请求"}
			}
			pendingMu.Lock()
			if pending != transaction {
				pendingMu.Unlock()
				circles.cancel(transaction)
				return control.Result{Message: "配对请求已取消或更换"}
			}
			if pending == transaction {
				pending = ""
				pendingKind = ""
				if panel != nil {
					panel.SetPending(nil)
				}
			}
			pendingMu.Unlock()
			status.PairingState = "awaiting_client_proof"
			if err := status.save(*dir); err != nil {
				return control.Result{Message: "无法保存配对进度"}
			}
			return control.Result{OK: true, Code: code, CodeKind: "circle", HideAt: time.Now().Add(time.Minute).UTC(), Message: "请在发起端输入此配对验证码"}
		}
		if action != "deny" {
			generationCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			code, err = generateCode(generationCtx)
			stop()
			if err != nil {
				return control.Result{Message: "本机验证码生成失败"}
			}
		}
		if action != "code" {
			pendingMu.Lock()
			same := pending == transaction
			pendingMu.Unlock()
			if !same {
				return control.Result{Message: "登录请求已取消或发生变化"}
			}
			if active.RespondToSignIn(transaction, action == "allow") != nil {
				return control.Result{Message: "Apple 未接受本次登录响应"}
			}
			if action == "deny" {
				circles.cancel(transaction)
			}
			pendingMu.Lock()
			if pending == transaction {
				pending = ""
				pendingKind = ""
				if panel != nil {
					panel.SetPending(nil)
				}
			}
			pendingMu.Unlock()
		}
		return control.Result{OK: true, Code: code, HideAt: time.Now().Add(time.Minute).UTC(), Message: "操作已完成"}
	}
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) || (*listenFor > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
				return nil
			}
			return ctx.Err()
		case err := <-errorsCh:
			if ctx.Err() != nil {
				return nil
			}
			push.Close()
			status.PushConnected, status.PushIssue = false, "reconnecting"
			pendingMu.Lock()
			pending = ""
			pendingKind = ""
			circles = newCircleManager(accountID, client.DSID)
			if status.PairingState != "" {
				status.PairingState = "interrupted"
			}
			if panel != nil {
				panel.SetPending(nil)
			}
			pendingMu.Unlock()
			if saveErr := status.save(*dir); saveErr != nil {
				return saveErr
			}
			fmt.Fprintln(os.Stderr, "Push connection ended; retaining the authenticated session and retrying:", err)
			scheduleReconnect()
		case <-reconnectTick:
			reconnectTick = nil
			saved := push.State()
			go func() {
				candidate, err := reconnectPush(ctx, saved.Token, func() (pushTransport, error) {
					if *native {
						return aps.NewNativeClient()
					}
					return aps.NewClient(saved)
				})
				select {
				case reconnected <- pushConnection{client: candidate, err: err}:
				case <-ctx.Done():
					if candidate != nil {
						candidate.Close()
					}
				}
			}()
		case result := <-reconnected:
			if result.err != nil {
				if errors.Is(result.err, errPushTokenChanged) {
					status.PushIssue = "token_changed"
				} else {
					scheduleReconnect()
				}
			} else {
				push = result.client
				status.PushConnected, status.PushIssue = true, ""
				status.PushReconnectCount++
				reconnectDelay = 2 * time.Second
				listen(push)
				fmt.Fprintln(os.Stderr, "Push connection restored with the existing token; account session retained.")
			}
			if err := status.save(*dir); err != nil {
				return err
			}
		case command := <-panelCommands:
			if command.Context.Err() != nil {
				continue
			}
			if (command.Action.Action == "allow" || command.Action.Action == "deny") && command.RequestID == "" {
				command.Reply <- control.Result{Message: "请重新查看当前登录请求"}
				continue
			}
			command.Reply <- performAction(command.Context, command.Action.Action, command.RequestID, command.Code)
		case <-refresh.C:
			result := renew()
			if status.VerificationRequired {
				return errVerificationRequired
			}
			if result.OK {
				fmt.Printf("Session renewed. Device query succeeded: count=%d, installation_visible=%t.\n", status.DeviceCount, status.InstallationVisible)
			} else {
				fmt.Fprintln(os.Stderr, "Session renewal failed; retaining the existing connection.")
			}
		case event := <-events:
			if event.Unsupported {
				status.UnsupportedNoticeCount++
				status.LastUnsupportedNoticeAt = time.Now().UTC()
				status.PushIssue = "unsupported_idms_notification"
				_ = status.save(*dir)
				fmt.Fprintf(os.Stderr, "An unsupported IDMS notification arrived (size=%d); payload was discarded and the APS connection was kept alive.\n", event.NoticeSize)
				continue
			}
			if event.AccountID != "" && event.AccountID != accountID {
				continue
			}
			pendingMu.Lock()
			if event.Command == 100 {
				preserveCircle := pending == event.TransactionID && pendingKind == "circle"
				if pending != event.TransactionID {
					pendingKind = ""
				}
				pending = event.TransactionID
				if panel != nil && !preserveCircle {
					panel.SetPending(&control.Pending{ID: requestID(pending), Kind: pendingKind, ReceivedAt: time.Now().UTC()})
				}
			}
			if event.Command == 400 && pending == event.PreviousTransactionID {
				pending = ""
				pendingKind = ""
				if panel != nil {
					panel.SetPending(nil)
				}
			}
			pendingMu.Unlock()
			if event.Command == 700 {
				if event.CircleStep == 1 {
					if err := circles.remember(event); err != nil {
						status.PairingState = "request_rejected"
						fmt.Fprintln(os.Stderr, "Circle initial request rejected; no approval or code exchange performed.")
					} else if circles.pending(event.TransactionID) != nil {
						pendingMu.Lock()
						pending = event.TransactionID
						pendingKind = "circle"
						if panel != nil {
							panel.SetPending(&control.Pending{ID: requestID(pending), Kind: "circle", PairingID: requestID(event.CircleTransactionID), ReceivedAt: time.Now().UTC()})
						}
						pendingMu.Unlock()
						status.PairingState = "awaiting_approval"
					}
				} else {
					phase, err := circles.handle(event, sendCircle, freshCircleCode)
					if err != nil {
						status.PairingState = "exchange_failed"
						fmt.Fprintln(os.Stderr, "Circle exchange failed; account session retained.")
					} else {
						status.PairingState = phase
						fmt.Fprintln(os.Stderr, "Circle progress:", phase)
					}
				}
				status.PairingUnsupported = false
				if err := status.save(*dir); err != nil {
					return err
				}
			}
			if event.Command == 1200 {
				fmt.Fprintln(os.Stderr, "Apple delivered a device-status notification for this account. This does not verify code generation.")
			}
			if event.Command == 100 {
				if *background {
					fmt.Fprintln(os.Stderr, "An AuthKit sign-in request arrived for this account; no automatic approval in background mode.")
				} else if *listenOnly {
					fmt.Fprintln(os.Stderr, "An AuthKit sign-in request arrived for this account. Code generation is unavailable in observation mode.")
				} else {
					fmt.Fprintln(os.Stderr, "A sign-in request arrived for this account. Type allow to display a code, deny to reject it, or press Enter to ignore.")
				}
			}
			if event.Command == 400 {
				circles.cancel(event.PreviousTransactionID)
				fmt.Fprintln(os.Stderr, "Apple cancelled a sign-in request.")
			}
		case choice, ok := <-choices:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				choices = nil
				fmt.Fprintln(os.Stderr, "Sign-in input closed; session and reception remain active.")
				continue
			}
			if choice != "devices" && choice != "refresh" && choice != "code" && choice != "allow" && choice != "deny" {
				continue
			}
			result := performAction(ctx, choice, "", "")
			if result.Code != "" {
				fmt.Println("Apple verification code:", result.Code)
			} else {
				fmt.Fprintln(os.Stderr, result.Message)
			}
		}
	}
}

func codeRequest(device *appleservices.Device) aps.CodeRequest {
	return aps.CodeRequest{DeviceID: (anisette.State{Identifier: device.Identifier}).DeviceID(), Identifier: device.Identifier, ProvisioningBlob: device.ProvisioningBlob}
}

func savePush(path string, state aps.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return errors.New("could not save sensitive push state")
	}
	return nil
}

// loadProbeSpecs reads an optional diagnostic probe list from
// <dir>/probe-urls.json. It is a local experiment aid: when the file exists
// the storage-probe action probes those HTTPS endpoints instead of the
// built-in list, so probes can iterate without restarting the worker (each
// restart costs one Apple login). Anything malformed disables the override.
func loadProbeSpecs(dir string) []icloud.ProbeSpec {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "probe-urls.json"))
	if err != nil || len(data) == 0 || len(data) > 64*1024 {
		return nil
	}
	var specs []icloud.ProbeSpec
	if json.Unmarshal(data, &specs) != nil || len(specs) == 0 || len(specs) > 40 {
		return nil
	}
	for _, spec := range specs {
		if !strings.HasPrefix(spec.URL, "https://") {
			return nil
		}
	}
	return specs
}
