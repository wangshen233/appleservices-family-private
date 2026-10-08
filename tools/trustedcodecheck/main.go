// trustedcodecheck tests one owned account's local trusted Mac without approving
// sign-in notifications or performing registration, terms, or purchase calls.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/internal/backgroundexec"
	"github.com/Laky-64/appleservices/nativeadi"
	"github.com/google/uuid"
	"howett.net/plist"
)

type listener interface {
	Connect(context.Context) error
	State() aps.State
	Listen(context.Context, func(aps.Event) error) error
	Close() error
}

func hardwareToken(path string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(path, "hw_info.plist"))
	if err != nil {
		return nil, errors.New("hardware unavailable")
	}
	var hardware struct {
		Push struct {
			Token []byte `plist:"token"`
		} `plist:"push"`
	}
	if _, err := plist.Unmarshal(data, &hardware); err != nil || len(hardware.Push.Token) != 32 {
		return nil, errors.New("hardware has no valid APS token")
	}
	return hardware.Push.Token, nil
}

func native(ctx context.Context, library, profile string, device appleservices.Device, token []byte) (*nativeadi.Client, error) {
	c, err := nativeadi.NewClient(nativeadi.Config{LibraryPath: library, ProfileDir: profile, Python: "python", Identifier: device.Identifier, Serial: device.SerialNumber, PushToken: token})
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func gsaClient(provider gsa.AnisetteProvider, token []byte) (*gsa.Client, error) {
	c := gsa.NewClient(provider)
	c.EnableNativeDeviceAuthentication()
	if err := c.SetPushToken(token); err != nil {
		return nil, err
	}
	return c, nil
}

func run() error {
	dir := flag.String("dir", "", "existing trusted receiver account")
	work := flag.String("work", "", "existing owner-only private test directory")
	wait := flag.Duration("listen-for", 45*time.Second, "bounded delivery observation after triggering one request")
	pushOnly := flag.Bool("push-only", false, "observe delivery without submitting a code or completing the requester login")
	savedReceiver := flag.Bool("saved-receiver-binding", false, "listen using the bound account and APS state without renewing receiver GSA")
	flag.Parse()
	if *dir == "" || *work == "" || *wait < time.Second || *wait > time.Minute {
		return errors.New("account, private work directory and bounded listen time required")
	}
	if *savedReceiver && !*pushOnly {
		return errors.New("saved receiver binding is observation-only and requires push-only")
	}
	username, password := os.Getenv("APPLESERVICES_APPLEID"), os.Getenv("APPLESERVICES_PASSWORD")
	os.Unsetenv("APPLESERVICES_APPLEID")
	os.Unsetenv("APPLESERVICES_PASSWORD")
	if username == "" || password == "" {
		return errors.New("credentials required through environment")
	}
	var session appleservices.Session
	data, err := os.ReadFile(filepath.Join(*dir, "session.json"))
	if err != nil || json.Unmarshal(data, &session) != nil || !strings.EqualFold(session.AppleID, username) {
		return errors.New("receiver account binding failed")
	}
	var device appleservices.Device
	data, err = os.ReadFile(filepath.Join(*dir, "device.json"))
	if err != nil || json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 {
		return errors.New("receiver device binding failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	profile := filepath.Join(*dir, "native-adi")
	token, err := hardwareToken(profile)
	if err != nil {
		return err
	}
	receiver, err := native(ctx, library, profile, device, token)
	if err != nil {
		return err
	}
	defer receiver.Close()
	adsid := session.DSID
	if !*savedReceiver {
		receiverGSA, err := gsaClient(receiver, token)
		if err != nil {
			return err
		}
		receiverAuth, err := receiverGSA.Login(username, password)
		if err != nil {
			return err
		}
		if receiverAuth.NeedsTwoFactor {
			return errors.New("receiver session needs 2FA; no code requested")
		}
		adsid, _ = receiverAuth.SessionPayload["adsid"].(string)
		if adsid == "" || adsid != session.DSID {
			return errors.New("receiver authenticated account mismatch")
		}
		fmt.Println("receiver GSA authenticated; same trusted Mac; no new 2FA required")
	} else {
		fmt.Println("receiver uses saved account binding; GSA trust is not inferred")
	}
	var push listener
	if saved, err := os.ReadFile(filepath.Join(*dir, "push.json")); err == nil {
		var state aps.State
		if json.Unmarshal(saved, &state) != nil || !bytes.Equal(state.Token, token) {
			return errors.New("receiver private APS binding failed")
		}
		push, err = aps.NewClient(state)
		if err != nil {
			return errors.New("receiver private APS unavailable")
		}
	} else if os.IsNotExist(err) {
		push, err = aps.NewNativeClient()
		if err != nil {
			return err
		}
	} else {
		return errors.New("receiver push state unavailable")
	}
	if err := push.Connect(ctx); err != nil {
		return err
	}
	defer push.Close()
	if !bytes.Equal(push.State().Token, token) {
		return errors.New("receiver APS token changed; test stopped")
	}
	fmt.Println("receiver push connected; token matches trusted Mac; listening without approving notifications")
	listenCtx, stopListen := context.WithCancel(ctx)
	defer stopListen()
	events := make(chan aps.Event, 32)
	listenErr := make(chan error, 1)
	go func() {
		listenErr <- push.Listen(listenCtx, func(event aps.Event) error {
			if event.AccountID == adsid || event.Unsupported {
				select {
				case events <- event:
				default:
				}
			}
			return nil
		})
	}()
	requesterDir := filepath.Join(*work, "requester")
	if err := os.MkdirAll(requesterDir, 0700); err != nil {
		return errors.New("private requester directory unavailable")
	}
	devicePath := filepath.Join(requesterDir, "device.json")
	var requesterDevice appleservices.Device
	if data, err := os.ReadFile(devicePath); err == nil {
		if json.Unmarshal(data, &requesterDevice) != nil || len(requesterDevice.Identifier) != 16 || bytes.Equal(requesterDevice.Identifier, device.Identifier) {
			return errors.New("private requester binding is invalid or shared")
		}
	} else if os.IsNotExist(err) {
		id := uuid.New()
		requesterDevice = appleservices.Device{Identifier: append([]byte(nil), id[:]...), SerialNumber: fmt.Sprintf("AS%X", id[:5])}
		encoded, _ := json.Marshal(requesterDevice)
		if err := os.WriteFile(devicePath, encoded, 0600); err != nil {
			return errors.New("cannot save private requester binding")
		}
	} else {
		return errors.New("cannot read private requester binding")
	}
	requesterProfile, _ := filepath.Abs(filepath.Join(requesterDir, "native-adi"))
	requesterPushPath := filepath.Join(requesterDir, "push.json")
	helper := exec.CommandContext(ctx, "python", "tools/mac-push-check.py", "--profile", requesterProfile, "--device", devicePath, "--library", library, "--go-state-out", requesterPushPath)
	backgroundexec.Configure(helper)
	var output bytes.Buffer
	helper.Stdout, helper.Stderr = &output, io.Discard
	if _, err := os.Stat(requesterPushPath); os.IsNotExist(err) {
		if err := helper.Run(); err != nil {
			return errors.New("temporary diagnostic Mac APS setup failed")
		}
	} else if err != nil {
		return errors.New("temporary requester push state unavailable")
	}
	requesterToken, err := hardwareToken(requesterProfile)
	if err != nil || bytes.Equal(requesterToken, token) {
		return errors.New("temporary requester must have independent push identity")
	}
	requester, err := native(ctx, library, requesterProfile, requesterDevice, requesterToken)
	if err != nil {
		return err
	}
	defer requester.Close()
	client, err := gsaClient(requester, requesterToken)
	if err != nil {
		return err
	}
	// Start the observation before the new login; Apple may emit its sign-in
	// notice during SRP completion, before the explicit code request.
	for len(events) > 0 {
		<-events
	}
	auth, err := client.Login(username, password)
	if err != nil {
		return err
	}
	requesterADSID, _ := auth.SessionPayload["adsid"].(string)
	idms, _ := auth.SessionPayload["GsIdmsToken"].(string)
	if requesterADSID != adsid || idms == "" {
		return errors.New("temporary login has no bound validation context")
	}
	fmt.Printf("independent requester GSA password accepted; needs-2fa=%t; continuation=%s\n", auth.NeedsTwoFactor, auth.TwoFactorMethod)
	if err := client.RequestTrustedDeviceCode(adsid, idms); err != nil {
		return err
	}
	fmt.Println("one trusted-device verification request accepted; no SMS request or sign-in approval sent")
	validated := false
	if !*pushOnly {
		code, err := receiver.Code(ctx)
		if err != nil {
			return errors.New("receiver local code unavailable")
		}
		codeErr := client.SubmitTrustedDeviceCode(adsid, idms, code)
		code = ""
		if codeErr != nil {
			fmt.Printf("local code server validation failed: %v\n", codeErr)
		} else {
			fmt.Println("local code submission accepted; checking full requester authentication")
		}
		if codeErr == nil && auth.NeedsTwoFactor {
			verified := client.VerifiedLoginResult()
			if verified == nil {
				verified, err = client.Login(username, password)
			}
			if err == nil && verified != nil && !verified.NeedsTwoFactor {
				services, _ := verified.SessionPayload["t"].(map[string]any)
				pet, _ := services["com.apple.gs.idms.pet"].(map[string]any)
				value, _ := pet["token"].(string)
				validated = value != ""
			}
		}
	}
	deadline := time.NewTimer(*wait)
	defer deadline.Stop()
	notices, signins, cancellations, unparsed := 0, 0, 0, 0
	for {
		select {
		case event := <-events:
			if event.Unsupported {
				unparsed++
				continue
			}
			notices++
			if event.Command == 100 {
				signins++
			}
			if event.Command == 400 {
				cancellations++
			}
			fmt.Printf("same-account AuthKit notification received; command=%d; no approval sent\n", event.Command)
		case err := <-listenErr:
			if err != nil && listenCtx.Err() == nil {
				return errors.New("receiver listener failed during delivery observation")
			}
		case <-deadline.C:
			fmt.Printf("test complete; same-account-notices=%d; sign-in-notices=%d; cancellations=%d; unattributed-unparsed-notices=%d; local-code-tested=%t; local-code-proved-by-complete-2fa=%t; observation-seconds=%d; approvals-sent=0\n", notices, signins, cancellations, unparsed, !*pushOnly, validated, int(wait.Seconds()))
			return nil
		case <-ctx.Done():
			return errors.New("trusted code test timed out")
		}
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
