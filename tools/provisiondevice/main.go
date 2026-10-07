// Command provisiondevice registers a device identity with Apple's anisette
// service and prints the Machine ID it returns.
//
// The mobileme delegate refuses an identity Apple has never seen with
// "MachineId does not exist in DS", so a generated identifier is not usable
// until this exchange has completed once.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Laky-64/appleservices/anisette"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: provisiondevice <state-dir> [server]")
		os.Exit(2)
	}
	dir := os.Args[1]
	server := "https://ani.sidestore.io"
	if len(os.Args) > 2 {
		server = os.Args[2]
	}

	data, err := os.ReadFile(filepath.Join(dir, "device.json"))
	if err != nil {
		fmt.Println("device:", err)
		os.Exit(1)
	}
	var dev struct {
		Identifier   []byte
		SerialNumber string
	}
	if err := json.Unmarshal(data, &dev); err != nil {
		fmt.Println("decode:", err)
		os.Exit(1)
	}
	fmt.Printf("identifier: %x\n", dev.Identifier)
	fmt.Printf("serial:     %s\n", dev.SerialNumber)
	fmt.Printf("server:     %s\n\n", server)

	state := anisette.State{Identifier: dev.Identifier}
	client, err := anisette.NewClient(server)
	if err != nil {
		fmt.Println("client:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := client.Provision(ctx, &state); err != nil {
		fmt.Println("PROVISION FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("PROVISION OK")
	fmt.Printf("  adi_pb len = %d\n", len(state.AdiPB))

	headers, err := client.GetHeaders(state)
	if err != nil {
		fmt.Println("headers:", err)
		os.Exit(1)
	}
	for k, v := range headers {
		show := v
		if len(show) > 50 {
			show = show[:50] + "..."
		}
		fmt.Printf("  %s = %s\n", k, show)
	}
}
