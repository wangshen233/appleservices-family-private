package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Laky-64/appleservices"
)

var errVerificationRequired = errors.New("account verification is required; complete it in the dashboard")

type maintainedLogin interface {
	NeedsTwoFactor() bool
	Client() (*appleservices.Client, error)
	RegisterDevice(appleservices.DeviceRegistration) error
	AccountDevices() ([]appleservices.AccountDevice, error)
	SaveSession() error
	RespondToSignIn(string, bool) error
	BeginAccountValidation() error
	SubmitCode(appleservices.TwoFactorMethod, string) error
}

// A renewal is adopted only after authentication, registration and device-list
// access all succeed. Callers keep the old session on a transient error.
func renewSession(begin func() (maintainedLogin, error), accountID string, registration appleservices.DeviceRegistration) (maintainedLogin, []appleservices.AccountDevice, error) {
	return renewSessionMode(begin, accountID, registration, true)
}

func renewSessionMode(begin func() (maintainedLogin, error), accountID string, registration appleservices.DeviceRegistration, register bool) (maintainedLogin, []appleservices.AccountDevice, error) {
	login, err := begin()
	if err != nil {
		return nil, nil, err
	}
	if login.NeedsTwoFactor() {
		return nil, nil, errVerificationRequired
	}
	client, err := login.Client()
	if err != nil {
		return nil, nil, err
	}
	if client == nil || client.ADSID != accountID {
		return nil, nil, errors.New("session renewal returned a different account")
	}
	if register {
		if err := login.RegisterDevice(registration); err != nil {
			return nil, nil, err
		}
	}
	devices, err := login.AccountDevices()
	if err != nil {
		return nil, nil, err
	}
	if err := login.SaveSession(); err != nil {
		return nil, nil, err
	}
	return login, devices, nil
}

type runtimeStatus struct {
	PID                     int       `json:"pid"`
	Running                 bool      `json:"running"`
	PushConnected           bool      `json:"push_connected"`
	Authenticated           bool      `json:"authenticated"`
	VerificationRequired    bool      `json:"verification_required"`
	State                   string    `json:"state"`
	UpdatedAt               time.Time `json:"updated_at"`
	LastRefreshAt           time.Time `json:"last_refresh_at"`
	LastDeviceQueryAt       time.Time `json:"last_device_query_at"`
	DeviceCount             int       `json:"device_count"`
	InstallationVisible     bool      `json:"installation_visible"`
	RefreshCount            int       `json:"refresh_count"`
	PairingUnsupported      bool      `json:"pairing_unsupported"`
	PairingState            string    `json:"pairing_state,omitempty"`
	PushReconnectCount      int       `json:"push_reconnect_count"`
	UnsupportedNoticeCount  int       `json:"unsupported_notice_count"`
	LastUnsupportedNoticeAt time.Time `json:"last_unsupported_notice_at,omitempty"`
	PushIssue               string    `json:"push_issue,omitempty"`
	FailureStage            string    `json:"failure_stage,omitempty"`
}

func (s *runtimeStatus) setDevices(devices []appleservices.AccountDevice, name string) {
	s.DeviceCount = len(devices)
	s.InstallationVisible = false
	for _, device := range devices {
		s.InstallationVisible = s.InstallationVisible || device.Name == name
	}
	s.LastDeviceQueryAt = time.Now().UTC()
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".session-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (s *runtimeStatus) save(dir string) error {
	s.UpdatedAt = time.Now().UTC()
	if err := writeJSONFile(filepath.Join(dir, "runtime.json"), s); err != nil {
		return errors.New("could not save local runtime status")
	}
	return nil
}

func stopRequested(dir string, pid int) bool {
	path := filepath.Join(dir, "stop-request.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var request struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(data, &request) != nil || request.PID != pid {
		return false
	}
	os.Remove(path)
	return true
}

func watchStop(ctx context.Context, cancel context.CancelFunc, dir string, pid int) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if stopRequested(dir, pid) {
				cancel()
				return
			}
		}
	}
}
