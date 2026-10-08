// Command v2login calls Apple's v2 sign-in endpoint, the one a working client
// uses to obtain iCloud delegates.
//
// The project posts to /setup/iosbuddy/loginDelegates, which answers a refusal
// with a generic "server problem" message and no reason. The v2 endpoint takes a
// different request shape and reports localizedError and description instead.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"howett.net/plist"
)

type session struct {
	AppleID string            `json:"AppleID"`
	DSID    string            `json:"DSID"`
	Tokens  map[string]string `json:"Tokens"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: v2login <state-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		fmt.Println("session:", err)
		os.Exit(1)
	}
	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}

	pet := s.Tokens["com.apple.gs.idms.pet"]
	body := map[string]any{
		"delegates":        map[string]any{"com.apple.mobileme": map[string]any{}},
		"protocol_version": "1.0",
		"user_info": map[string]any{
			"client-id": "00000000-0000-0000-0000-000000000001",
			"language":  "en-US",
			"timezone":  "America/New_York",
		},
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		fmt.Println("encode:", err)
		os.Exit(1)
	}

	req, _ := http.NewRequest("POST", "https://setup.icloud.com/setup/signin/v2/login", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "text/plist")
	req.Header.Set("Accept-Encoding", "gzip")
	// The delegate exchange identifies as the iCloud helper running under
	// accountsd, not as the anisette provider's client.
	req.Header.Set("User-Agent", "com.apple.iCloudHelper/282 CFNetwork/1408.0.4 Darwin/22.5.0")
	req.Header.Set("X-Mme-Client-Info", "<MacBookPro18,3> <macOS;13.4.1;22F8> <com.apple.AOSKit/282 (com.apple.accountsd/113)>")
	req.Header.Set("X-Apple-ADSID", s.DSID)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.AppleID+":"+pet)))

	fmt.Printf("POST https://setup.icloud.com/setup/signin/v2/login\n")
	fmt.Printf("  X-Apple-ADSID = %s\n\n", s.DSID)
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		fmt.Println("request:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Printf("HTTP %d, %d bytes\n", resp.StatusCode, len(out))
	fmt.Printf("X-Apple-Reason-Code: %q\n\n", resp.Header.Get("X-Apple-Reason-Code"))
	fmt.Println(string(out))
}
