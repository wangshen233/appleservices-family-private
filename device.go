package appleservices

import (
	"errors"
	"github.com/Laky-64/appleservices/gsa"
	"strings"
)

type identityAnisette struct {
	provider gsa.AnisetteProvider
	serial   string
}

func (p identityAnisette) Headers() (map[string]string, error) {
	headers, err := p.provider.Headers()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(headers)+1)
	for key, value := range headers {
		if !strings.EqualFold(key, "X-Apple-I-SRL-NO") {
			result[key] = value
		}
	}
	result["X-Apple-I-SRL-NO"] = p.serial
	return result, nil
}

type AccountDevice = gsa.AccountDevice
type DeviceRegistration = gsa.DeviceRegistration

// RegisterDevice announces this installation to the account. It does not prove
// trusted-device status: callers must verify account-side state and real delivery.
func (l *Login) RegisterDevice(registration DeviceRegistration) error {
	if l.client == nil || l.needsTwoFactor {
		return errors.New("appleservices: complete iCloud authentication before device registration")
	}
	backend, ok := l.backend.(interface {
		RegisterDevice(map[string]any, gsa.DeviceRegistration) error
	})
	if !ok {
		return errors.New("appleservices: backend does not support device registration")
	}
	return backend.RegisterDevice(l.result.SessionPayload, registration)
}

func (l *Login) AccountDevices() ([]AccountDevice, error) {
	if l.client == nil || l.needsTwoFactor {
		return nil, errors.New("appleservices: complete iCloud authentication before reading device status")
	}
	backend, ok := l.backend.(interface {
		AccountDevices(map[string]any) ([]gsa.AccountDevice, error)
	})
	if !ok {
		return nil, errors.New("appleservices: backend does not support device status")
	}
	return backend.AccountDevices(l.result.SessionPayload)
}

func (l *Login) RespondToSignIn(transaction string, allow bool) error {
	if l.client == nil || l.needsTwoFactor {
		return errors.New("appleservices: complete authentication before responding to a sign-in")
	}
	backend, ok := l.backend.(interface {
		RespondToSignIn(map[string]any, string, bool) error
	})
	if !ok {
		return errors.New("appleservices: backend cannot respond to sign-in notifications")
	}
	return backend.RespondToSignIn(l.result.SessionPayload, transaction, allow)
}
