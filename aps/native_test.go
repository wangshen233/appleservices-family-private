package aps

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A local child process emulates only the bridge's JSON stream. This exercises
// the real reader/listener boundary where cmd700 used to kill reception.
func TestNativePairingStreamHelper(t *testing.T) {
	if os.Getenv("APS_TEST_PAIRING_STREAM") != "1" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.Encode(nativeMessage{Type: "ready", Token: make([]byte, 32)})
	for _, payload := range []string{`{"cmd":700,"adsid":"test"}`, `{"cmd":100,"adsid":"test","txnid":"test"}`, `{"cmd":1200,"adsid":"test"}`} {
		encoder.Encode(nativeMessage{Type: "event", Payload: []byte(payload)})
	}
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestNativePairingDoesNotEndFollowingNotifications(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestNativePairingStreamHelper$")
	command.Env = append(os.Environ(), "APS_TEST_PAIRING_STREAM=1")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &NativeClient{command: command, input: input, ready: make(chan nativeReady, 1), events: make(chan Event, 16), done: make(chan struct{})}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go c.read(output)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case ready := <-c.ready:
		if ready.err != nil {
			t.Fatal(ready.err)
		}
		c.mu.Lock()
		c.token = ready.token
		c.mu.Unlock()
	case <-ctx.Done():
		t.Fatal("bridge did not initialize")
	}
	var commands []int
	err = c.Listen(ctx, func(event Event) error {
		commands = append(commands, event.Command)
		if len(commands) == 3 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(commands, []int{700, 100, 1200}) {
		t.Fatalf("pairing interrupted notification stream: commands=%v err=%v", commands, err)
	}
}

func TestIDMSRealNoticeShapes(t *testing.T) {
	for _, alert := range []string{`"Sign in requested"`, `{"title":"Sign in requested","body":"test"}`} {
		event, err := ParseEvent([]byte(`{"cmd":100,"adsid":"test","txnid":"test","aps":{"alert":` + alert + `}}`))
		if err != nil || event.Command != 100 {
			t.Fatalf("failed to decode valid alert shape: %v", err)
		}
	}
	event, err := ParseEvent([]byte(`{"cmd":1200,"adsid":"test","aps":{"alert":""},"tddelta":{"items":[]}}`))
	if err != nil || event.Command != 1200 {
		t.Fatalf("device status notice failed: %v", err)
	}
	if _, err := ParseEvent([]byte(`{"cmd":9999}`)); !errors.Is(err, ErrUnhandledNotice) {
		t.Fatal("unhandled notice should be distinguishable from malformed messages")
	}
	if event, err := ParseEvent([]byte(`{"cmd":700}`)); err != nil || event.Command != 700 {
		t.Fatal("pairing notice must reach the handler without ending reception")
	}
}

func TestNativeDiagnosticRedaction(t *testing.T) {
	for _, message := range []nativeMessage{
		{Stage: "token secret-data", HRESULT: "80040154"},
		{Stage: "token", HRESULT: "secret-data"},
		{Stage: "token", HRESULT: "SECRETS!"},
	} {
		if strings.Contains(nativeBridgeError(message).Error(), "secret") || strings.Contains(nativeBridgeError(message).Error(), "SECRETS") {
			t.Fatal("native diagnostic leaked unvalidated text")
		}
	}
	if !strings.Contains(nativeBridgeError(nativeMessage{Stage: "callback", HRESULT: "80040154"}).Error(), "0x80040154") {
		t.Fatal("native diagnostic lost the COM error code")
	}
}

func TestInstalledNativePush(t *testing.T) {
	if os.Getenv("APPLESERVICES_TEST_NATIVE_PUSH") != "1" {
		t.Skip("requires installed iCloud; opt in to native daemon access")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	length, err := ProbeNativePush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if length != 32 {
		t.Fatal("native token has wrong length")
	}
}
