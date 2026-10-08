package nativeadi

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestAnisetteHeadersSurviveRestart checks stable identity fields across helper
// restarts. It does not establish Apple acceptance of an account login. This
// integration test is skipped unless its DLL and device variables are set.
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
	// X-Apple-I-MD is an OTP-like value and need not survive a restart.
	for _, key := range []string{"X-Apple-I-MD-M", "X-Mme-Device-Id", "X-Apple-I-MD-LU"} {
		if first[key] == "" {
			t.Errorf("%s is missing", key)
			continue
		}
		if first[key] != second[key] {
			t.Errorf("%s changed across restart; first-length=%d; second-length=%d", key, len(first[key]), len(second[key]))
		}
	}
}
