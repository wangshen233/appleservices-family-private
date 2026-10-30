package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
)

func TestFirstConnectionCreatesAccountBeforeMacPush(t *testing.T) {
	root := t.TempDir()
	worker, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Dir: root, Worker: worker, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	var identity []byte
	var accountDir string
	calls := 0
	server.provisionMacPush = func(_ context.Context, dir string) error {
		calls++
		if filepath.Dir(dir) != accountsDir(root) {
			t.Fatal("first connection used the shared root instead of an account directory")
		}
		data, err := os.ReadFile(filepath.Join(dir, "device.json"))
		if err != nil {
			t.Fatalf("push initialization started without a device identity: %v", err)
		}
		var device appleservices.Device
		if json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 || device.SerialNumber == "" {
			t.Fatal("push initialization received an invalid device identity")
		}
		if identity != nil && (!bytes.Equal(identity, data) || accountDir != dir) {
			t.Fatal("retry changed the account identity")
		}
		identity, accountDir = data, dir
		// Stop before launching the worker; no account credentials leave this test.
		return errors.New("simulated push connection failure")
	}
	connect := func(appleID string) control.Result {
		t.Helper()
		body, err := json.Marshal(credentials{AppleID: appleID, Password: "local-test-password"})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", server.URL()+"/api/start", bytes.NewReader(body))
		request.Header.Set("X-App-Token", server.token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		var result control.Result
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal("connection endpoint failed")
		}
		return result
	}
	if result := connect("first@example.invalid"); result.OK {
		t.Fatal("failed push setup reported a successful connection")
	}
	if calls != 2 || len(server.accounts) != 1 || identity == nil {
		t.Fatal("first connection did not register and initialize one account")
	}
	id := server.accounts[0].ID
	if result := connect("first@example.invalid"); result.OK {
		t.Fatal("failed retry reported success")
	}
	if calls != 4 || len(server.accounts) != 1 || server.accounts[0].ID != id {
		t.Fatal("retry duplicated or replaced the account")
	}
	if result := connect("second@example.invalid"); result.OK || calls != 4 || len(server.accounts) != 1 {
		t.Fatal("another account's credentials reached the existing identity")
	}
	registryData, err := os.ReadFile(registryPath(root))
	if err != nil || bytes.Contains(registryData, []byte("local-test-password")) {
		t.Fatal("registry is missing or persisted a password")
	}
	reopened, err := New(Config{Dir: root, Worker: worker, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.listener.Close()
	if len(reopened.accounts) != 1 || reopened.accounts[0].ID != id {
		t.Fatal("first account was lost after restart")
	}
}

func TestFirstConnectionPreservesUnreadableRegistry(t *testing.T) {
	root := t.TempDir()
	original := []byte("invalid-registry-preserve-for-recovery")
	if err := os.WriteFile(registryPath(root), original, 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Dir: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	result := server.Start(context.Background(), credentials{AppleID: "first@example.invalid", Password: "local-test-password"})
	if server.registryNote == "" || result.OK || result.Message != server.registryNote {
		t.Fatal("connection must surface the registry error instead of initializing another account")
	}
	after, err := os.ReadFile(registryPath(root))
	if err != nil || !bytes.Equal(original, after) || len(server.accounts) != 0 {
		t.Fatal("connection replaced the unreadable account registry")
	}
}
