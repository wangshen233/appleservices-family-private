// Command anisettecompare calls the iCloud delegate twice with the same session
// and device, changing only where the anisette headers come from.
//
// The project moved from a remote anisette server to the local native ADI module
// after a login that succeeded with the remote one, so the two providers are the
// main difference between a working and a failing delegate.
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

	"github.com/Laky-64/appleservices/anisette"
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
		fmt.Println("usage: anisettecompare <state-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	sessData, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		fmt.Println("session:", err)
		os.Exit(1)
	}
	var s session
	if err := json.Unmarshal(sessData, &s); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	devData, _ := os.ReadFile(filepath.Join(dir, "device.json"))
	var d device
	_ = json.Unmarshal(devData, &d)

	// Remote provider, keyed to the same saved identity.
	state := anisette.State{Identifier: d.Identifier, AdiPB: d.ProvisioningBlob}
	remote := anisette.NewProviderFromState(state, nil)
	remoteHeaders, err := remote.Headers()
	if err != nil {
		fmt.Println("remote anisette:", err)
	} else {
		call(dir, s, "REMOTE anisette", remoteHeaders)
	}

	// Local native ADI provider.
	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	client, err := nativeadi.NewClient(nativeadi.Config{
		LibraryPath: library, ProfileDir: filepath.Join(dir, "adi-cmp"),
		Python: "python", Identifier: d.Identifier, Serial: d.SerialNumber,
		PushToken: make([]byte, 32),
	})
	if err != nil {
		fmt.Println("native adi:", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		fmt.Println("native adi start:", err)
		return
	}
	defer client.Close()
	nativeHeaders, err := client.Headers()
	if err != nil {
		fmt.Println("native headers:", err)
		return
	}
	call(dir, s, "NATIVE anisette", nativeHeaders)
}

func call(dir string, s session, label string, headers map[string]string) {
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
		return
	}
	req, _ := http.NewRequest("POST", "https://setup.icloud.com/setup/iosbuddy/loginDelegates", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "text/plist")
	req.Header.Set("X-Apple-ADSID", s.DSID)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.AppleID+":"+pet)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	fmt.Printf("\n===== %s =====\n", label)
	for _, key := range []string{"X-Apple-I-MD-RINFO", "X-Apple-I-MD-LU", "X-Mme-Client-Info", "X-Apple-I-MD", "X-Apple-I-MD-M"} {
		fmt.Printf("  %-20s = %q\n", key, truncate(req.Header.Get(key), 60))
	}
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		fmt.Println("  request:", err)
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Printf("  HTTP %d, %d bytes\n", resp.StatusCode, len(out))
	fmt.Printf("  body: %s\n", collapse(string(out)))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func collapse(s string) string {
	out := make([]rune, 0, len(s))
	space := false
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' || r == ' ' {
			if !space {
				out = append(out, ' ')
				space = true
			}
			continue
		}
		space = false
		out = append(out, r)
	}
	if len(out) > 500 {
		out = out[:500]
	}
	return string(out)
}
