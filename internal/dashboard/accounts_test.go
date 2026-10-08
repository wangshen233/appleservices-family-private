package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/anisette"
)

// TestAddingAccountDoesNotOverwriteExistingAccount is the regression test for
// the reported bug: adding a second account used to relaunch the worker in the
// same directory, replacing the first account's session and device identity.
func TestAddingAccountDoesNotOverwriteExistingAccount(t *testing.T) {
	root := t.TempDir()
	// Seed a legacy single-account directory the way the old code left it.
	legacy := root
	session := map[string]string{"appleId": "first@example.com", "DSID": "111"}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(legacy, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// A provisioned device identity, shaped exactly like the real one: the
	// worker refuses a directory whose device cannot be loaded.
	device := appleservices.Device{
		Identifier:       bytes.Repeat([]byte{0x5a}, 16),
		ProvisioningBlob: []byte("provisioning-blob"),
		SerialNumber:     "SERIAL-ONE",
	}
	data, _ = json.Marshal(device)
	os.WriteFile(filepath.Join(legacy, "device.json"), data, 0600)

	server, err := New(Config{Dir: root, Worker: filepath.Join(root, "worker.exe"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	if len(server.accounts) != 1 {
		t.Fatalf("legacy account not adopted as the first account: %d", len(server.accounts))
	}
	firstDir := server.accounts[0].Dir
	if filepath.Clean(firstDir) == filepath.Clean(root) {
		t.Fatal("legacy state was reused instead of moved into its own directory")
	}
	original, _ := os.ReadFile(filepath.Join(firstDir, "session.json"))
	if string(original) == "" {
		t.Fatal("legacy session was not carried over")
	}

	// A second account must never share the first account's device identity:
	// Apple answers ec=-29004 "possible environment mismatch" when two logins
	// present the same device, breaking both. It must also leave the first
	// account untouched, whether it is accepted or refused.
	firstDevice, _ := os.ReadFile(filepath.Join(firstDir, "device.json"))
	before := len(server.accounts)
	_, id := server.addAccount(context.Background(), credentials{AppleID: "second@example.com", Password: "pw"})

	after, _ := os.ReadFile(filepath.Join(firstDir, "session.json"))
	if string(after) != string(original) {
		t.Fatal("adding an account modified the existing account's session")
	}
	if again, _ := os.ReadFile(filepath.Join(firstDir, "device.json")); string(again) != string(firstDevice) {
		t.Fatal("adding an account modified the existing account's device identity")
	}
	if id != "" {
		second := server.lookup(id)
		blob, err := os.ReadFile(filepath.Join(second.Dir, "device.json"))
		if err == nil && string(blob) == string(firstDevice) {
			t.Fatal("second account was given a copy of the first account's device identity")
		}
	} else if len(server.accounts) != before {
		t.Fatal("a refused account was still registered")
	}

	// The registry must survive a restart with the accounts intact.
	reopened, err := New(Config{Dir: root, Worker: filepath.Join(root, "worker.exe"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.listener.Close()
	if len(reopened.accounts) != len(server.accounts) {
		t.Fatalf("registry lost accounts across restart: %d vs %d", len(reopened.accounts), len(server.accounts))
	}
	// A duplicate email resolves to the existing entry rather than adding one.
	if _, dupID := reopened.addAccount(context.Background(), credentials{AppleID: "first@example.com", Password: "pw"}); dupID != reopened.accounts[0].ID {
		t.Fatal("duplicate account was not mapped back to the existing entry")
	}
	if len(reopened.accounts) != len(server.accounts) {
		t.Fatal("duplicate add created extra state")
	}
}

// TestNewIdentityIsNeverACopy verifies per-account state isolation and prevents
// fake provisioning markers from being introduced in a newly created identity.
func TestNewIdentityIsNeverACopy(t *testing.T) {
	freshDir := t.TempDir()
	if err := provisionIdentity(freshDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(freshDir, "device.json"))
	if err != nil {
		t.Fatal(err)
	}
	var device appleservices.Device
	if err := json.Unmarshal(data, &device); err != nil {
		t.Fatal(err)
	}
	if len(device.Identifier) != 16 || len(device.ProvisioningBlob) != 0 {
		t.Fatal("new identity contains fake provisioning credentials")
	}
	dir := t.TempDir()
	// A directory that already has an identity must not be re-provisioned.
	if err := os.WriteFile(filepath.Join(dir, "device.json"), []byte(`{"Identifier":"c29tZS1pZGVudGl0eQ=="}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := provisionIdentity(dir); err == nil {
		t.Fatal("an existing identity was overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "device.json"))
	if !strings.Contains(string(after), "c29tZS1pZGVudGl0eQ==") {
		t.Fatal("the existing identity was modified")
	}

	// A fresh identity is 16 bytes and unrelated to any other account's.
	state := anisette.NewState()
	if len(state.Identifier) != 16 {
		t.Fatalf("generated identifier is %d bytes, want 16", len(state.Identifier))
	}
	other := anisette.NewState()
	if string(state.Identifier) == string(other.Identifier) {
		t.Fatal("two generated identities collided")
	}
}
