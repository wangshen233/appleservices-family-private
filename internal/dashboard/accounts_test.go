package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
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

	// Registering a second account must allocate a different directory and
	// leave the first account's session byte-identical.
	res, id := server.addAccount(context.Background(), credentials{AppleID: "second@example.com", Password: "pw"})
	if id == "" {
		t.Fatalf("second account was not allocated a directory: ok=%v msg=%q", res.OK, res.Message)
	}
	if len(server.accounts) != 2 {
		t.Fatalf("expected two accounts, got %d", len(server.accounts))
	}
	after, _ := os.ReadFile(filepath.Join(firstDir, "session.json"))
	if string(after) != string(original) {
		t.Fatal("adding an account modified the existing account's session")
	}
	second := server.lookup(id)
	if second == nil || second.Dir == firstDir {
		t.Fatal("second account reused the first account's directory")
	}
	if _, err := os.Stat(filepath.Join(second.Dir, "session.json")); err == nil {
		t.Fatal("second account inherited the first account's session")
	}

	// The registry must survive a restart with both accounts intact.
	reopened, err := New(Config{Dir: root, Worker: filepath.Join(root, "worker.exe"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.listener.Close()
	if len(reopened.accounts) != 2 {
		t.Fatalf("registry lost accounts across restart: %d", len(reopened.accounts))
	}
	// A duplicate email is rejected rather than silently replacing the entry.
	_, dupID := reopened.addAccount(context.Background(), credentials{AppleID: "first@example.com", Password: "pw"})
	if dupID != reopened.accounts[0].ID {
		t.Fatal("duplicate account was not mapped back to the existing entry")
	}
	if len(reopened.accounts) != 2 {
		t.Fatal("duplicate add created extra state")
	}
}
