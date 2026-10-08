// Command gsacheck runs only the GrandSlam init step with the anisette headers
// a saved device produces, to show whether Apple accepts the identity before any
// verification code is requested.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Laky-64/appleservices/anisette"
	"howett.net/plist"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: gsacheck <state-dir> <appleid>")
		os.Exit(2)
	}
	dir, appleID := os.Args[1], os.Args[2]
	data, err := os.ReadFile(filepath.Join(dir, "device.json"))
	if err != nil {
		fmt.Println("device:", err)
		os.Exit(1)
	}
	var dev struct {
		Identifier       []byte
		ProvisioningBlob []byte
		SerialNumber     string
	}
	if err := json.Unmarshal(data, &dev); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	fmt.Printf("identifier: %x\n", dev.Identifier)
	fmt.Printf("blob:       %d bytes\n", len(dev.ProvisioningBlob))

	state := anisette.State{Identifier: dev.Identifier, AdiPB: dev.ProvisioningBlob}
	p := anisette.NewProviderFromState(state, nil)
	headers, err := p.Headers()
	if err != nil {
		fmt.Println("headers:", err)
		os.Exit(1)
	}

	body := map[string]any{
		"Header": map[string]any{"Version": "1.0.1"},
		"Request": map[string]any{
			"o":  "init",
			"u":  appleID,
			"ps": []string{"s2k", "s2k_fo"},
		},
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		fmt.Println("encode:", err)
		os.Exit(1)
	}
	req, _ := http.NewRequest("POST", "https://gsa.apple.com/grandslam/GsService2", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "text/x-xml-plist")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "akd/1.0 CFNetwork/978.0.7 Darwin/18.7.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		fmt.Println("request:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Printf("\nGSA init: HTTP %d, %d bytes\n", resp.StatusCode, len(out))
	if len(out) > 0 {
		fmt.Println(string(out[:min(len(out), 900)]))
	}
}
