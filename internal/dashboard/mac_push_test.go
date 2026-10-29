package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMacPushStateRequiresCompleteAccountPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "device.json"),
		filepath.Join(dir, "native-adi", "hw_info.plist"),
		filepath.Join(dir, "push.json"),
	}
	for index, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("private-state"), 0600); err != nil {
			t.Fatal(err)
		}
		if index < len(paths)-1 && macPushStateReady(dir) {
			t.Fatal("partial Mac APS state was accepted")
		}
	}
	if !macPushStateReady(dir) {
		t.Fatal("complete Mac APS state was rejected")
	}
}

func TestEnsureMacPushStateRepairsBeforeWorkerStart(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device.json"), []byte("device"), 0600); err != nil {
		t.Fatal(err)
	}
	called := 0
	server := &Server{provisionMacPush: func(_ context.Context, got string) error {
		called++
		if filepath.Clean(got) != filepath.Clean(dir) {
			t.Fatal("provisioner received another account directory")
		}
		if err := os.MkdirAll(filepath.Join(dir, "native-adi"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "native-adi", "hw_info.plist"), []byte("hardware"), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "push.json"), []byte("push"), 0600)
	}}
	if err := server.ensureMacPushState(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("provisioner called %d times", called)
	}
	if err := server.ensureMacPushState(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("complete state was provisioned again")
	}
}

func TestEnsureMacPushStateRetriesOneTransientFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device.json"), []byte("device"), 0600); err != nil {
		t.Fatal(err)
	}
	called := 0
	server := &Server{provisionMacPush: func(_ context.Context, got string) error {
		called++
		if filepath.Clean(got) != filepath.Clean(dir) {
			t.Fatal("provisioner received another account directory")
		}
		if called == 1 {
			return context.DeadlineExceeded
		}
		if err := os.MkdirAll(filepath.Join(dir, "native-adi"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "native-adi", "hw_info.plist"), []byte("hardware"), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "push.json"), []byte("push"), 0600)
	}}
	if err := server.ensureMacPushState(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatalf("provisioner called %d times", called)
	}
}
