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
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "trusted-device:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", "./state", "existing device/session directory")
	name := flag.String("name", "appleservices", "name announced to Apple")
	certificatePath := flag.String("push-cert", "", "Apple-issued client push certificate PEM file")
	keyPath := flag.String("push-key", "", "matching client push private key PEM file")
	codeProvider := flag.String("code-provider", "", "absolute path to a working local ADI code-provider executable")
	server := flag.String("anisette", "https://ani.sidestore.io", "anisette server for this identity")
	method := flag.String("twofactor", "sms", "initial sign-in method: sms or trusted-device")
	check := flag.Bool("check", false, "check local prerequisites without contacting Apple")
	native := flag.Bool("native-push", false, "use the installed iCloud for Windows APS daemon")
	probeNative := flag.Bool("probe-native-push", false, "check native token and local callback; no account registration")
	listenOnly := flag.Bool("listen-only", false, "register and observe notifications; do not approve requests or generate codes")
	listenFor := flag.Duration("listen-for", 0, "stop after this duration; 0 keeps listening")
	flag.Parse()
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
	if *method != "sms" && *method != "trusted-device" {
		return errors.New("twofactor must be sms or trusted-device")
	}
	if *listenFor < 0 {
		return errors.New("listen-for must not be negative")
	}
	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*dir) == "" {
		return errors.New("name and state directory are required")
	}
	if !*listenOnly {
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
	var push interface {
		Connect(context.Context) error
		State() aps.State
		Listen(context.Context, func(aps.Event) error) error
		Close() error
	}
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
		fmt.Println("Local inputs available. Apple trust and code delivery have not been verified.")
		return nil
	}
	appleID, password := os.Getenv("APPLESERVICES_APPLEID"), os.Getenv("APPLESERVICES_PASSWORD")
	if appleID == "" || password == "" {
		return errors.New("APPLESERVICES_APPLEID and APPLESERVICES_PASSWORD are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
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
		return err
	}
	defer push.Close()
	if !*native {
		if err := savePush(savedPath, push.State()); err != nil {
			return err
		}
	}
	identity := sha256.Sum256(device.Identifier)
	serial := device.SerialNumber
	if serial == "" {
		serial = "AS" + strings.ToUpper(fmt.Sprintf("%x", identity[:5]))
	}
	login, err := appleservices.BeginLogin(appleservices.Credentials{AppleID: appleID, Password: password}, store,
		appleservices.WithAnisetteServers(*server), appleservices.WithDeviceSerialNumber(serial), appleservices.WithPushToken(push.State().Token))
	if err != nil {
		return err
	}
	input := bufio.NewReader(os.Stdin)
	if login.NeedsTwoFactor() {
		factor := appleservices.SMS
		if *method == "trusted-device" {
			factor = appleservices.TrustedDevice
		}
		if err := login.RequestCode(factor); err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Enter the initial sign-in verification code: ")
		code, err := input.ReadString('\n')
		if err != nil {
			return errors.New("could not read initial verification code")
		}
		if err := login.SubmitCode(factor, strings.TrimSpace(code)); err != nil {
			return err
		}
	}
	client, err := login.Client()
	if err != nil {
		return err
	}
	if !*listenOnly {
		generationCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		_, err = aps.CodeFromProcess(generationCtx, *codeProvider, codeRequest(device))
		stop()
		if err != nil {
			return fmt.Errorf("code-generation prerequisite failed before account registration: %w", err)
		}
	}
	if err := login.RegisterDevice(appleservices.DeviceRegistration{Name: *name, PushToken: push.State().Token}); err != nil {
		return err
	}
	fmt.Println("Apple accepted the registration request. Waiting for real AuthKit delivery; trusted-device reception is not yet verified.")
	devices, err := login.AccountDevices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not verify account device status:", err)
	} else {
		for _, entry := range devices {
			if entry.Name == *name {
				fmt.Println("An entry with the announced name is visible in the account device list; this alone does not prove trust.")
				if entry.IsTrustedDevice != nil {
					fmt.Printf("Account-reported trusted status: %t.\n", *entry.IsTrustedDevice)
				}
			}
		}
	}
	if *listenOnly {
		fmt.Println("Observation mode: no codes will be generated and no sign-in requests will be approved.")
	}
	var pendingMu sync.Mutex
	var pending string
	events := make(chan aps.Event, 16)
	errorsCh := make(chan error, 1)
	go func() {
		errorsCh <- push.Listen(ctx, func(event aps.Event) error {
			if event.AccountID != "" && event.AccountID != client.ADSID {
				return nil
			}
			pendingMu.Lock()
			if event.Command == 400 && pending != event.PreviousTransactionID {
				pendingMu.Unlock()
				return nil
			}
			if event.Command == 100 {
				pending = event.TransactionID
			}
			if event.Command == 400 && pending == event.PreviousTransactionID {
				pending = ""
			}
			pendingMu.Unlock()
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	var choices <-chan string
	if !*listenOnly {
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
	for {
		select {
		case <-ctx.Done():
			if *listenOnly && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil
			}
			return ctx.Err()
		case err := <-errorsCh:
			return err
		case event := <-events:
			if event.Command == 1200 {
				fmt.Fprintln(os.Stderr, "Apple delivered a device-status notification for this account. This does not verify code generation.")
			}
			if event.Command == 100 {
				if *listenOnly {
					fmt.Fprintln(os.Stderr, "An AuthKit sign-in request arrived for this account. Code generation is unavailable in observation mode.")
				} else {
					fmt.Fprintln(os.Stderr, "A sign-in request arrived for this account. Type allow to display a code, deny to reject it, or press Enter to ignore.")
				}
			}
			if event.Command == 400 {
				fmt.Fprintln(os.Stderr, "Apple cancelled a sign-in request.")
			}
		case choice, ok := <-choices:
			if !ok {
				return errors.New("sign-in input closed")
			}
			if choice != "allow" && choice != "deny" {
				continue
			}
			pendingMu.Lock()
			transaction := pending
			pendingMu.Unlock()
			if transaction == "" {
				fmt.Fprintln(os.Stderr, "No pending sign-in request.")
				continue
			}
			code := ""
			if choice == "allow" {
				generationCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				code, err = aps.CodeFromProcess(generationCtx, *codeProvider, codeRequest(device))
				stop()
				if err != nil {
					return err
				}
			}
			pendingMu.Lock()
			stillPending := pending == transaction
			pendingMu.Unlock()
			if !stillPending {
				fmt.Fprintln(os.Stderr, "The request changed or was cancelled; no code displayed.")
				continue
			}
			if err := login.RespondToSignIn(transaction, choice == "allow"); err != nil {
				return err
			}
			if choice == "allow" {
				fmt.Println("Apple verification code:", code)
			}
			pendingMu.Lock()
			if pending == transaction {
				pending = ""
			}
			pendingMu.Unlock()
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
