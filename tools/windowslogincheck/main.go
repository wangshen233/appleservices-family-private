// Windows ADI/GSA diagnostic. Keeps a separate identity/checkpoint and does not
// change the dashboard session or use the official iCloud user interface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/uuid"
)

type provider struct {
	ctx                                                       context.Context
	python, helper, dll, deviceID, appVersion, authkitVersion string
}

func (p *provider) Headers() (map[string]string, error) {
	output, err := exec.CommandContext(p.ctx, p.python, p.helper, p.dll, "--headers-pipe").Output()
	if err != nil {
		return nil, errors.New("Windows CoreADI helper failed; run its status-only diagnostic")
	}
	var result struct {
		Headers map[string]string `json:"headers"`
		Facts   map[string]any    `json:"facts"`
	}
	if json.Unmarshal(output, &result) != nil || result.Headers["X-Apple-I-MD"] == "" || result.Headers["X-Apple-I-MD-M"] == "" {
		return nil, errors.New("Windows CoreADI did not return a complete anisette pair")
	}
	version, ok := result.Facts["windows-version"].(string)
	if !ok || version == "" {
		return nil, errors.New("Windows OS version unavailable")
	}
	h := result.Headers
	h["User-Agent"] = "iCloud/" + p.appVersion
	h["X-Mme-Client-Info"] = fmt.Sprintf("<PC> <Windows; %s> <com.apple.AuthKitWin/%s (com.apple.iCloud/%s)>", version, p.authkitVersion, p.appVersion)
	h["X-Mme-Device-Id"] = p.deviceID
	h["X-Apple-I-Client-Time"] = time.Now().UTC().Format(time.RFC3339)
	h["X-Apple-I-TimeZone"], h["X-Apple-Locale"], h["loc"] = "UTC", "en_US", "en_US"
	// CPD metadata is copied by GSA; doRequest only uses the consumer UA/info
	// as HTTP headers. Never enable its Mac AppleIDSettings identity option.
	h["capp"], h["cbid"], h["svct"] = "iCloud", "com.apple.iCloud", "iCloud"
	return h, nil
}

func run() error {
	dir := flag.String("dir", "", "bound target account directory")
	dll := flag.String("adi-dll", "", "absolute staged installed Windows CoreADI64.dll")
	python := flag.String("python", "python", "64-bit Python")
	helper := flag.String("helper", "tools/windows-anisette.py", "private native helper")
	checkpoint := flag.String("checkpoint", "", "new private Windows GSA checkpoint (not activation success)")
	exchange := flag.String("exchange", "none", "explicit diagnostic: none, windows-aos, windows-iosbuddy")
	credential := flag.String("credential", "pet", "explicit credential: pet or icloud-auth (Windows AOS only)")
	dump := flag.String("dump", "", "optional private raw exchange response")
	codeFile := flag.String("sms-code-file", "", "new private file for this Windows challenge")
	phoneID := flag.Int("sms-phone-id", 0, "explicit trusted phone diagnostic; zero fetches metadata")
	trustedFallback := flag.Bool("trusted-fallback", false, "try trusted-device verification on this same challenge if SMS request fails")
	appVersion := flag.String("app-version", "15.10", "explicit installed iCloud consumer version")
	authkitVersion := flag.String("authkit-version", "430.4", "explicit installed AuthKitWin version")
	flag.Parse()
	if *dir == "" || !filepath.IsAbs(*dll) {
		return errors.New("dir and absolute adi-dll required")
	}
	if *exchange != "none" && *exchange != "windows-aos" && *exchange != "windows-iosbuddy" {
		return errors.New("unsupported Windows exchange")
	}
	if *credential != "pet" && (*credential != "icloud-auth" || *exchange != "windows-aos") {
		return errors.New("unsupported Windows credential experiment")
	}
	username, password := os.Getenv("APPLESERVICES_APPLEID"), os.Getenv("APPLESERVICES_PASSWORD")
	if username == "" || password == "" {
		return errors.New("credentials must be provided through environment")
	}
	var bound appleservices.Session
	data, err := os.ReadFile(filepath.Join(*dir, "session.json"))
	if err != nil || json.Unmarshal(data, &bound) != nil || !strings.EqualFold(bound.AppleID, username) {
		return errors.New("target account binding failed")
	}
	if *checkpoint != "" {
		if _, err := os.Stat(*checkpoint); !os.IsNotExist(err) {
			return errors.New("checkpoint must be a new private path")
		}
	}
	if *codeFile != "" {
		if _, err := os.Stat(*codeFile); !os.IsNotExist(err) {
			return errors.New("SMS code path must not already exist")
		}
	}
	identityDir := filepath.Join(*dir, "windows-adi")
	if err := os.MkdirAll(identityDir, 0700); err != nil {
		return err
	}
	identityPath := filepath.Join(identityDir, "device-id.txt")
	data, err = os.ReadFile(identityPath)
	if os.IsNotExist(err) {
		data = []byte(strings.ToUpper(uuid.New()))
		err = os.WriteFile(identityPath, data, 0600)
	}
	if err != nil {
		return errors.New("Windows identity unavailable")
	}
	deviceID := strings.TrimSpace(string(data))
	if len(deviceID) != 36 {
		return errors.New("invalid persisted Windows diagnostic identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := &provider{ctx: ctx, python: *python, helper: *helper, dll: *dll, deviceID: deviceID, appVersion: *appVersion, authkitVersion: *authkitVersion}
	client := gsa.NewClient(p)
	fmt.Println("Windows machine ADI; separate stable diagnostic identity; fresh GSA; no Mac hardware fields")
	auth, err := client.Login(username, password)
	if err != nil {
		return err
	}
	if auth.NeedsTwoFactor {
		if *codeFile == "" {
			return errors.New("Windows GSA password accepted; new 2FA required")
		}
		adsid, _ := auth.SessionPayload["adsid"].(string)
		idms, _ := auth.SessionPayload["GsIdmsToken"].(string)
		if adsid == "" || idms == "" {
			return errors.New("missing challenge binding")
		}
		var requestErr error
		if *phoneID > 0 {
			requestErr = client.RequestSMSCodeForPhone(adsid, idms, *phoneID)
		} else {
			requestErr = client.RequestSMSCode(adsid, idms)
		}
		trusted := false
		if requestErr != nil {
			if !*trustedFallback {
				return requestErr
			}
			fmt.Println(requestErr)
			if err := client.RequestTrustedDeviceCode(adsid, idms); err != nil {
				return err
			}
			trusted = true
			fmt.Println("Windows trusted-device request accepted; awaiting code for this live challenge")
		} else {
			fmt.Println("Windows SMS request accepted; awaiting code for this live challenge")
		}
		var code string
		for code == "" {
			data, err := os.ReadFile(*codeFile)
			if err == nil {
				code = strings.TrimSpace(string(data))
				break
			}
			if !os.IsNotExist(err) {
				return errors.New("private SMS file unavailable")
			}
			select {
			case <-ctx.Done():
				return errors.New("Windows SMS challenge wait expired")
			case <-time.After(200 * time.Millisecond):
			}
		}
		if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return errors.New("invalid six-digit code")
		}
		if trusted {
			err = client.SubmitTrustedDeviceCode(adsid, idms, code)
		} else {
			err = client.SubmitSMSCode(adsid, idms, code)
		}
		if err != nil {
			return err
		}
		fmt.Println("Windows verification code accepted")
		auth = client.VerifiedLoginResult()
		if auth == nil {
			auth, err = client.Login(username, password)
			if err != nil {
				return err
			}
		}
		if auth.NeedsTwoFactor {
			return errors.New("Windows GSA still requires 2FA after verification")
		}
	}
	services, _ := auth.SessionPayload["t"].(map[string]any)
	fresh := appleservices.Session{AppleID: username, Tokens: map[string]string{}}
	fresh.DSID, _ = auth.SessionPayload["adsid"].(string)
	for service, value := range services {
		if fields, ok := value.(map[string]any); ok {
			if token, ok := fields["token"].(string); ok {
				fresh.Tokens[service] = token
			}
		}
	}
	if fresh.DSID == "" || fresh.Tokens["com.apple.gs.idms.pet"] == "" {
		return errors.New("Windows GSA has no bound PET")
	}
	exchangeToken := fresh.Tokens["com.apple.gs.idms.pet"]
	if *credential == "icloud-auth" {
		token, err := client.FetchAppToken(auth.SessionPayload, "com.apple.gs.icloud.auth")
		if err != nil {
			return err
		}
		fresh.Tokens["com.apple.gs.icloud.auth"] = token
		exchangeToken = token
		fmt.Println("Windows iCloud service app-token accepted; activation unverified")
	}
	if *checkpoint != "" {
		encoded, err := json.Marshal(fresh)
		if err != nil {
			return errors.New("cannot encode private Windows checkpoint")
		}
		if err := os.WriteFile(*checkpoint, encoded, 0600); err != nil {
			return errors.New("cannot save private Windows checkpoint")
		}
	}
	fmt.Printf("Windows GSA accepted; PET present=true; app-token-count=%d; iCloud activation unverified\n", len(fresh.Tokens))
	if *exchange != "none" {
		headers, err := p.Headers()
		if err != nil {
			return err
		}
		os.Setenv("APPLESERVICES_DELEGATE_PATH", *exchange)
		if *dump != "" {
			os.Setenv("APPLESERVICES_DELEGATE_DUMP", *dump)
		}
		tokens, err := icloud.FetchDelegateTokens(headers, username, fresh.DSID, exchangeToken)
		if err != nil {
			return err
		}
		fmt.Println("Windows iCloud exchange accepted; MME token present=true")
		info, err := icloud.FetchStorageInfo(ctx, tokens.ServiceData, username, tokens.DSID, fresh.DSID, tokens.MMEAuthToken, headers)
		if err != nil {
			return err
		}
		fmt.Printf("Windows iCloud quota accepted; total-bytes=%d; paid-bytes=%d\n", info.TotalBytes, info.PaidBytes)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
