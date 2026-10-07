package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/anisette"
)

// provisionIdentity creates a device identity for a new account.
//
// Every account needs its own identity. Presenting one identity from two
// accounts is what Apple rejects with ec=-29004 "possible environment
// mismatch", and the rejection invalidates that identity for every account that
// used it, so an identity is always freshly generated and never copied.
//
// The identity is a random 16-byte identifier. The local ADI module provisions
// it on first use: it registers the identifier and derives the anisette headers
// from it, which is why a freshly generated identifier works without any
// registration step here.
func provisionIdentity(dir string) error {
	target := filepath.Join(dir, "device.json")
	if _, err := os.Stat(target); err == nil {
		return errors.New("该账号目录已有设备身份")
	}
	state := anisette.NewState()
	if len(state.Identifier) != 16 {
		return errors.New("无法生成设备身份")
	}
	device := appleservices.Device{
		Identifier: state.Identifier,
		// The native ADI path derives its credentials from the identifier and
		// never reads this field, but the worker requires it to be non-empty.
		// It is only consumed by the external code-provider path, which this
		// panel does not use.
		ProvisioningBlob: []byte("native-adi"),
		SerialNumber:     "AS" + upperHex(state.Identifier[:5]),
	}
	data, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		return errors.New("无法序列化设备身份")
	}
	return os.WriteFile(target, data, 0o600)
}

// upperHex renders the first bytes of an identity in the serial-number shape
// the worker derives when a saved identity carries no serial.
func upperHex(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}
