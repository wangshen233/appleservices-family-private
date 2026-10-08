package appleservices

import (
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/gsa"
)

type registrationBackend struct {
	fakeBackend
	registrations int
	failure       error
}

func (b *registrationBackend) RegisterDevice(map[string]any, gsa.DeviceRegistration) error {
	b.registrations++
	return b.failure
}

func TestPrepareRegistrationNeedsGSAAndCanPrecedeICloud(t *testing.T) {
	b := &registrationBackend{}
	l := &Login{backend: b, result: testResult(false), adsid: "test-adsid"}
	if err := l.PrepareDeviceRegistration(DeviceRegistration{}); err != nil || b.registrations != 1 {
		t.Fatal("GSA registration incorrectly requires an iCloud token", err)
	}
	l.needsTwoFactor = true
	if err := l.PrepareDeviceRegistration(DeviceRegistration{}); err == nil || b.registrations != 1 {
		t.Fatal("registration attempted before 2FA")
	}
	l.needsTwoFactor = false
	b.failure = errors.New("postdata refused")
	if !errors.Is(l.PrepareDeviceRegistration(DeviceRegistration{}), b.failure) {
		t.Fatal("postdata refusal swallowed")
	}
	if l.client != nil {
		t.Fatal("postdata reported iCloud activation")
	}
}
