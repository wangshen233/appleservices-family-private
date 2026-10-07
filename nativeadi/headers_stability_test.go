package nativeadi

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestAnisetteHeadersSurviveRestart pins down a property the worker depends on:
// restarting it must not change the device's anisette headers.
//
// This matters because a changed identity would make Apple reject the login
// with ec=-29004 "possible environment mismatch" — the account would appear
// broken after an ordinary restart. The test disproves that for the local ADI
// module, so a restart is safe for the device identity.
//
// It is skipped unless APPLESERVICES_ADI_DLL and APPLESERVICES_ADI_DEVICE are
// set, because it starts the real module.
func TestAnisetteHeadersSurviveRestart(t *testing.T) {
	library := os.Getenv("APPLESERVICES_ADI_DLL")
	devicePath := os.Getenv("APPLESERVICES_ADI_DEVICE")
	if library == "" || devicePath == "" {
		t.Skip("set APPLESERVICES_ADI_DLL and APPLESERVICES_ADI_DEVICE to run")
	}
	data, err := os.ReadFile(devicePath)
	if err != nil {
		t.Skip("device state unavailable")
	}
	var device struct{ Identifier []byte }
	if json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 {
		t.Skip("device state unusable")
	}

	profile := t.TempDir()
	token := make([]byte, 32)
	headers := func() map[string]string {
		client, err := NewClient(Config{LibraryPath: library, ProfileDir: profile,
			Python: "python", Identifier: device.Identifier, Serial: "ASTEST000000", PushToken: token})
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := client.Start(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		h, err := client.Headers()
		if err != nil {
			t.Fatalf("headers: %v", err)
		}
		return h
	}

	first, second := headers(), headers()
	for _, key := range []string{"X-Apple-I-MD", "X-Apple-I-MD-M", "X-Mme-Device-Id", "X-Apple-I-MD-LU"} {
		if first[key] == "" {
			t.Errorf("%s is missing", key)
			continue
		}
		if first[key] != second[key] {
			t.Errorf("%s changed across restart, so a restarted worker would present a different device\n  first:  %q\n  second: %q",
				key, first[key], second[key])
		}
	}
}
