// Command delegatedebug calls the iCloud delegate with a saved session and real
// anisette headers, printing Apple's unmodified response.
//
// The worker reports the delegate failure as a short message and discards the
// body, which hides the reason Apple gives. This tool keeps the raw response so
// the cause is visible.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Laky-64/appleservices/nativeadi"
	"howett.net/plist"
)

type session struct {
	AppleID string            `json:"AppleID"`
	DSID    string            `json:"DSID"`
	Tokens  map[string]string `json:"Tokens"`
}

type device struct {
	Identifier       []byte
	SerialNumber     string
	ProvisioningBlob []byte
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: delegatedebug <state-dir> [setup-host]")
		os.Exit(2)
	}
	dir := os.Args[1]
	host := "https://setup.icloud.com"
	if len(os.Args) > 2 {
		host = os.Args[2]
	}

	sessData, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		fmt.Println("session:", err)
		os.Exit(1)
	}
	var s session
	if err := json.Unmarshal(sessData, &s); err != nil {
		fmt.Println("session decode:", err)
		os.Exit(1)
	}

	devData, err := os.ReadFile(filepath.Join(dir, "device.json"))
	if err != nil {
		fmt.Println("device:", err)
		os.Exit(1)
	}
	var d device
	if err := json.Unmarshal(devData, &d); err != nil {
		fmt.Println("device decode:", err)
		os.Exit(1)
	}

	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	pushToken := make([]byte, 32)
	client, err := nativeadi.NewClient(nativeadi.Config{
		LibraryPath: library,
		ProfileDir:  filepath.Join(dir, "native-adi-debug"),
		Python:      "python",
		Identifier:  d.Identifier,
		Serial:      d.SerialNumber,
		PushToken:   pushToken,
	})
	if err != nil {
		fmt.Println("adi client:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		fmt.Println("adi start:", err)
		os.Exit(1)
	}
	defer client.Close()
	headers, err := client.Headers()
	if err != nil {
		fmt.Println("adi headers:", err)
		os.Exit(1)
	}
	fmt.Println("--- anisette headers produced ---")
	for k, v := range headers {
		show := v
		if len(show) > 40 {
			show = show[:40] + "..."
		}
		fmt.Printf("  %s = %s\n", k, show)
	}

	pet := s.Tokens["com.apple.gs.idms.pet"]
	body := map[string]any{
		"apple-id":  s.AppleID,
		"delegates": map[string]any{"com.apple.mobileme": map[string]any{}},
		"password":  pet,
		"client-id": "00000000-0000-0000-0000-000000000001",
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		fmt.Println("encode:", err)
		os.Exit(1)
	}

	req, _ := http.NewRequest("POST", host+"/setup/iosbuddy/loginDelegates", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "text/plist")
	req.Header.Set("X-Apple-ADSID", s.DSID)
	req.Header.Set("Cookie", "termsAccepted=true")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.AppleID+":"+pet)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	httpClient := &http.Client{Timeout: 45 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("request:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	fmt.Printf("\n=== POST %s/setup/iosbuddy/loginDelegates ===\n", host)
	fmt.Printf("HTTP %d, %d bytes\n", resp.StatusCode, len(out))
	fmt.Printf("X-Apple-Request-Debug: %q\n", resp.Header.Get("X-Apple-Request-Debug"))
	fmt.Printf("X-Responding-Instance: %q\n\n", resp.Header.Get("X-Responding-Instance"))
	fmt.Println("--- body ---")
	fmt.Println(string(out))
}
