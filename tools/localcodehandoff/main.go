// localcodehandoff writes a bound diagnostic Mac's current six-digit code to
// a new owner-only file for an already running authentication challenge.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/nativeadi"
	"howett.net/plist"
)

func run() error {
	dir := flag.String("dir", "", "bound account directory")
	out := flag.String("out", "", "new owner-only code handoff file")
	flag.Parse()
	if *dir == "" || *out == "" {
		return errors.New("bound account and new private output are required")
	}
	var device appleservices.Device
	data, err := os.ReadFile(filepath.Join(*dir, "device.json"))
	if err != nil || json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 {
		return errors.New("bound device unavailable")
	}
	var hardware struct {
		Push struct {
			Token []byte `plist:"token"`
		} `plist:"push"`
	}
	profile := filepath.Join(*dir, "native-adi")
	data, err = os.ReadFile(filepath.Join(profile, "hw_info.plist"))
	if err != nil {
		return errors.New("bound hardware unavailable")
	}
	if _, err = plist.Unmarshal(data, &hardware); err != nil || len(hardware.Push.Token) != 32 {
		return errors.New("bound APS token unavailable")
	}
	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	client, err := nativeadi.NewClient(nativeadi.Config{LibraryPath: library, ProfileDir: profile, Python: "python", Identifier: device.Identifier, Serial: device.SerialNumber, PushToken: hardware.Push.Token})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		return err
	}
	defer client.Close()
	code, err := client.Code(ctx)
	if err != nil {
		return errors.New("local verification code unavailable")
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("private output must be a new file")
	}
	if _, err = f.WriteString(code); err != nil {
		f.Close()
		return errors.New("cannot write private handoff")
	}
	code = ""
	if err := f.Close(); err != nil {
		return errors.New("cannot close private handoff")
	}
	fmt.Println("local code written to live private handoff")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
