package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/internal/control"
)

func TestDashboardRejectsCrossOriginAndMissingCapability(t *testing.T) {
	server, err := New(Config{Dir: t.TempDir(), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	for _, test := range []struct{ name, token, host, origin string }{
		{"missing-token", "", "", ""}, {"wrong-token", "wrong", "", ""},
		{"foreign-host", server.token, "attacker.example", ""}, {"cross-origin", server.token, "", "http://attacker.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", server.URL()+"/api/view", nil)
			request.Header.Set("X-App-Token", test.token)
			if test.host != "" {
				request.Host = test.host
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			if recorder.Code != 403 {
				t.Fatal("cross-origin dashboard access accepted")
			}
		})
	}
	request := httptest.NewRequest("GET", server.URL()+"/api/view", nil)
	request.Header.Set("X-App-Token", server.token)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != 200 || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authorized dashboard request failed")
	}
}

func TestDashboardFiltersRuntimeSecretsAndBindsDaemonPID(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Dir: dir, Worker: executable, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	controller, err := control.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	data, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "running": true, "authenticated": true, "state": "online", "private_password": "do-not-disclose"})
	if os.WriteFile(filepath.Join(dir, "runtime.json"), data, 0600) != nil {
		t.Fatal("state write failed")
	}
	controller.SetDevices([]control.Device{{Name: "test-device", Installation: true}})
	result := server.snapshot(context.Background())
	encoded, _ := json.Marshal(result)
	if !result.ControlReady || len(result.Devices) != 1 || strings.Contains(string(encoded), "do-not-disclose") {
		t.Fatal("public state leaked or live daemon was not attached")
	}
	record := controller.Record
	record.PID++
	data, _ = json.Marshal(record)
	os.WriteFile(filepath.Join(dir, "control.json"), data, 0600)
	if _, err := server.record(); err == nil {
		t.Fatal("stale daemon capability accepted")
	}
	// Restore the capability so the fixture can remove it on shutdown.
	data, _ = json.Marshal(controller.Record)
	os.WriteFile(filepath.Join(dir, "control.json"), data, 0600)
}

func TestDashboardControlAddressCannotReachExternalHosts(t *testing.T) {
	dir := t.TempDir()
	server, err := New(Config{Dir: dir, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	for _, target := range []string{"https://127.0.0.1:8080", "http://attacker.example:8080", "http://127.0.0.1:8080/path", "http://user@127.0.0.1:8080", "http://127.0.0.1:8080?secret=yes"} {
		data, _ := json.Marshal(control.Record{PID: os.Getpid(), URL: target, Token: strings.Repeat("a", 64)})
		os.WriteFile(filepath.Join(dir, "control.json"), data, 0600)
		if _, err := server.record(); err == nil {
			t.Fatal("unsafe control destination accepted")
		}
	}
	parsed, _ := url.Parse(server.URL())
	if parsed.Hostname() != "127.0.0.1" {
		t.Fatal("dashboard listener was public")
	}
}

func TestDeadSavedPIDDoesNotClaimOnlineOrSkipLogin(t *testing.T) {
	dir := t.TempDir()
	server, err := New(Config{Dir: dir, Worker: filepath.Join(dir, "worker.exe"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	const deadPID = 2147483647
	data, _ := json.Marshal(Status{PID: deadPID, Running: true, Authenticated: true, PushConnected: true, State: "online"})
	os.WriteFile(filepath.Join(dir, "runtime.json"), data, 0600)
	data, _ = json.Marshal(control.Record{PID: deadPID, URL: "http://127.0.0.1:1", Token: strings.Repeat("a", 64)})
	os.WriteFile(filepath.Join(dir, "control.json"), data, 0600)
	status := server.readStatus()
	if status.Running || status.Authenticated || status.PushConnected || status.State == "online" {
		t.Fatal("dead saved PID presented as an online worker")
	}
	result := server.Start(context.Background(), credentials{})
	if result.OK || result.Message == "已连接到现有后台" {
		t.Fatal("stale capability prevented a new login")
	}
}

func TestLastProcessErrorDoesNotExposeLogSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.err")
	if err := os.WriteFile(path, []byte("first\n\n  second   failure\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := lastProcessError(path); got == "" || strings.Contains(got, "second") {
		t.Fatalf("got %q", got)
	}
}

func TestReconnectRejectsDifferentAccountBeforeStartingWorker(t *testing.T) {
	s := &Server{}
	a := &Account{AppleID: "owner@example.test", Dir: t.TempDir()}
	result := s.startAccount(context.Background(), a, credentials{AppleID: "other@example.test", Password: "test"})
	if result.OK || !strings.Contains(result.Message, "不一致") {
		t.Fatal("different account accepted")
	}
	entries, _ := os.ReadDir(a.Dir)
	if len(entries) != 0 {
		t.Fatal("rejected login changed account state")
	}
}
