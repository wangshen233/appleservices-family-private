package aps

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

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
	if _, err := ParseEvent([]byte(`{"cmd":700}`)); err == nil || errors.Is(err, ErrUnhandledNotice) {
		t.Fatal("unimplemented pairing must remain an explicit error")
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
