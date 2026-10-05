package nativeadi

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRejectUnverifiedLibrary(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("Windows x64 validation")
	}
	path := filepath.Join(t.TempDir(), "unverified.dll")
	if err := os.WriteFile(path, []byte("not the signed release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLibrary(path); err == nil {
		t.Fatal("unverified native code would be loaded")
	}
	if err := ValidateLibrary("relative.dll"); err == nil {
		t.Fatal("relative library path accepted")
	}
}

func TestBridgeDoesNotInheritAccountCredentials(t *testing.T) {
	t.Setenv("APPLESERVICES_APPLEID", "private-account")
	t.Setenv("APPLESERVICES_PASSWORD", "private-password")
	t.Setenv("APPLESERVICES_NATIVE_TEST", "preserved")
	seen := false
	for _, entry := range bridgeEnvironment() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "APPLESERVICES_APPLEID") || strings.EqualFold(key, "APPLESERVICES_PASSWORD") {
			t.Fatal("account credentials inherited by native helper")
		}
		seen = seen || entry == "APPLESERVICES_NATIVE_TEST=preserved"
	}
	if !seen {
		t.Fatal("unrelated configuration lost")
	}
}

func TestReplyBindingAndRedaction(t *testing.T) {
	identity := uuid.New()
	for _, message := range []reply{
		{ID: 8, DeviceID: identity.String()},
		{ID: 7, DeviceID: uuid.NewString()},
		{ID: 7, DeviceID: "private-metadata"},
		{Error: "private-metadata native error"},
	} {
		c := &Client{deviceID: identity, replies: make(chan reply, 1), done: make(chan struct{})}
		c.replies <- message
		_, err := c.receive(context.Background(), 7)
		if err == nil || strings.Contains(err.Error(), "private-metadata") {
			t.Fatal("reply must be bound to the request and device with redacted errors")
		}
	}
	c := &Client{deviceID: identity, replies: make(chan reply, 1), done: make(chan struct{})}
	c.replies <- reply{ID: 7, DeviceID: strings.ToUpper(identity.String()), Ready: true}
	if _, err := c.receive(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.receive(ctx, 8); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled native request did not stop")
	}
}

type discardInput struct{ io.Writer }

func (discardInput) Close() error { return nil }

func fakeClient(message reply) *Client {
	identity := uuid.New()
	message.ID, message.DeviceID = 1, identity.String()
	c := &Client{deviceID: identity, command: &exec.Cmd{}, input: discardInput{io.Discard},
		replies: make(chan reply, 1), done: make(chan struct{})}
	c.replies <- message
	return c
}

func TestGeneratedCodeValidation(t *testing.T) {
	for _, code := range []string{"12345", "1234567", "12345x", "１２３４５６"} {
		if _, err := fakeClient(reply{Code: code}).Code(context.Background()); err == nil {
			t.Fatal("invalid native output accepted")
		}
	}
	code, err := fakeClient(reply{Code: "000123"}).Code(context.Background())
	if err != nil || code != "000123" {
		t.Fatal("leading zero was lost")
	}
}

func TestHeaderIdentityBinding(t *testing.T) {
	c := fakeClient(reply{Headers: map[string]string{"X-Mme-Device-Id": uuid.NewString()}})
	if _, err := c.Headers(); err == nil {
		t.Fatal("headers from a different provisioning identity accepted")
	}
	c = fakeClient(reply{Headers: map[string]string{}})
	message := <-c.replies
	message.Headers["X-Mme-Device-Id"] = c.deviceID.String()
	c.replies <- message
	if _, err := c.Headers(); err == nil {
		t.Fatal("incomplete native authentication headers accepted")
	}
}
