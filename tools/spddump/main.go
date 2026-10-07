// Command spddump prints the service tokens Apple returned for an account, which
// is the authoritative statement of which services it has enabled.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: spddump <state-dir>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1] + "/session.json")
	if err != nil {
		fmt.Println("session:", err)
		os.Exit(1)
	}
	var s struct {
		AppleID string            `json:"AppleID"`
		DSID    string            `json:"DSID"`
		Tokens  map[string]string `json:"Tokens"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	fmt.Println("AppleID:", s.AppleID)
	fmt.Println("DSID:   ", s.DSID)
	fmt.Println()
	keys := make([]string, 0, len(s.Tokens))
	for k := range s.Tokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("tokens (%d):\n", len(keys))
	for _, k := range keys {
		fmt.Printf("  %-45s len=%d\n", k, len(s.Tokens[k]))
	}
	fmt.Println()
	has := func(name string) bool { return strings.TrimSpace(s.Tokens[name]) != "" }
	for _, svc := range []string{
		"com.apple.gs.icloud.auth",
		"com.apple.gs.icloud.storage.buy",
		"com.apple.gs.icloud.family.auth",
		"com.apple.gs.idms.pet",
		"com.apple.gs.idms.hb",
		"com.apple.gs.idms.ln",
	} {
		fmt.Printf("  %-45s %v\n", svc, has(svc))
	}
}
