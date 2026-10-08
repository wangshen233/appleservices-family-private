package delegateprobe

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/nativeadi"
	"howett.net/plist"
)

// This tool reuses a saved PET once. A refusal from a saved token cannot
// establish the result of a fresh authentication experiment.
func Run(defaultProfile string) error {
	profile := flag.String("profile", defaultProfile, "iosbuddy, prefpane, v2 or legacy-v2")
	sessionPath := flag.String("session", "", "private fresh GSA checkpoint for this same account")
	termsAccepted := flag.Bool("terms-accepted", false, "send the reference cookie after a verified terms success acknowledgement")
	flag.Parse()
	if flag.NArg() != 1 {
		return fmt.Errorf("usage: delegate probe [-profile profile] <state-dir>")
	}
	dir, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid state directory")
	}
	var session appleservices.Session
	boundPath := filepath.Join(dir, "session.json")
	data, err := os.ReadFile(boundPath)
	if err != nil || json.Unmarshal(data, &session) != nil {
		return fmt.Errorf("saved session unavailable")
	}
	if *sessionPath != "" {
		var fresh appleservices.Session
		data, err = os.ReadFile(*sessionPath)
		if err != nil || json.Unmarshal(data, &fresh) != nil || !strings.EqualFold(fresh.AppleID, session.AppleID) {
			return fmt.Errorf("fresh checkpoint is not bound to this account")
		}
		session = fresh
	}
	pet := session.Tokens["com.apple.gs.idms.pet"]
	if pet == "" || session.AppleID == "" || session.DSID == "" {
		return fmt.Errorf("saved session has no PET/account binding")
	}
	var device appleservices.Device
	data, err = os.ReadFile(filepath.Join(dir, "device.json"))
	if err != nil || json.Unmarshal(data, &device) != nil {
		return fmt.Errorf("device state unavailable")
	}
	// Use the same hardware/profile and APS token as the worker. Zero tokens
	// and a new diagnostic profile were confounding the earlier experiments.
	nativeProfile := filepath.Join(dir, "native-adi")
	data, err = os.ReadFile(filepath.Join(nativeProfile, "hw_info.plist"))
	if err != nil {
		return fmt.Errorf("existing native hardware profile unavailable")
	}
	var hardware struct {
		Push struct {
			Token []byte `plist:"token"`
		} `plist:"push"`
	}
	if _, err := plist.Unmarshal(data, &hardware); err != nil || len(hardware.Push.Token) != 32 {
		return fmt.Errorf("saved APS token unavailable")
	}
	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	client, err := nativeadi.NewClient(nativeadi.Config{LibraryPath: library, ProfileDir: nativeProfile,
		Python: "python", Identifier: device.Identifier, Serial: device.SerialNumber, PushToken: hardware.Push.Token})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		return err
	}
	defer client.Close()
	headers, err := client.Headers()
	if err != nil {
		return err
	}
	if *termsAccepted {
		headers["Cookie"] = "termsAccepted=true"
	}
	os.Setenv("APPLESERVICES_DELEGATE_PATH", *profile)
	fmt.Printf("profile=%s; source=native; saved-PET=true; no automatic retries\n", *profile)
	tokens, err := icloud.FetchDelegateTokens(headers, session.AppleID, session.DSID, pet)
	if err != nil {
		return err
	}
	fmt.Printf("delegate accepted; mme-token-present=%t\n", tokens.MMEAuthToken != "")
	info, err := icloud.FetchStorageInfo(ctx, tokens.ServiceData, session.AppleID, tokens.DSID, session.DSID, tokens.MMEAuthToken, headers)
	if err != nil {
		return err
	}
	fmt.Printf("quota accepted; total-bytes=%d; paid-bytes=%d\n", info.TotalBytes, info.PaidBytes)
	return nil
}
