package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/icloud"
)

type fakeMaintainedLogin struct {
	challenge                      bool
	account                        string
	listErr                        error
	saveErr                        error
	registered, saved, clientCalls int
}

func (s *fakeMaintainedLogin) NeedsTwoFactor() bool { return s.challenge }
func (s *fakeMaintainedLogin) Client() (*appleservices.Client, error) {
	s.clientCalls++
	return &appleservices.Client{ADSID: s.account}, nil
}
func (s *fakeMaintainedLogin) RegisterDevice(appleservices.DeviceRegistration) error {
	s.registered++
	return nil
}
func (s *fakeMaintainedLogin) AccountDevices() ([]appleservices.AccountDevice, error) {
	return []appleservices.AccountDevice{{Name: "test-installation"}}, s.listErr
}
func (s *fakeMaintainedLogin) SaveSession() error { s.saved++; return s.saveErr }
func (s *fakeMaintainedLogin) RespondToSignIn(string, bool) error {
	panic("maintenance must not approve requests")
}
func (s *fakeMaintainedLogin) BeginAccountValidation() error                          { return nil }
func (s *fakeMaintainedLogin) SubmitCode(appleservices.TwoFactorMethod, string) error { return nil }

func TestRenewalRequiresFullSameAccountAuthentication(t *testing.T) {
	for _, test := range []struct {
		name      string
		login     fakeMaintainedLogin
		wantErr   bool
		wantSaves int
	}{
		{"verified", fakeMaintainedLogin{account: "test-account"}, false, 1},
		{"challenge", fakeMaintainedLogin{account: "test-account", challenge: true}, true, 0},
		{"different-account", fakeMaintainedLogin{account: "different"}, true, 0},
		{"list-failed", fakeMaintainedLogin{account: "test-account", listErr: errors.New("test")}, true, 0},
		{"save-failed", fakeMaintainedLogin{account: "test-account", saveErr: errors.New("test")}, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			login := test.login
			updated, devices, err := renewSession(func() (maintainedLogin, error) { return &login, nil }, "test-account", appleservices.DeviceRegistration{})
			if (err != nil) != test.wantErr || login.saved != test.wantSaves {
				t.Fatal("unverified renewal was adopted or not checkpointed")
			}
			if test.wantErr && updated != nil {
				t.Fatal("failed renewal replaced the active login")
			}
			if !test.wantErr && (updated != &login || len(devices) != 1) {
				t.Fatal("renewal results lost")
			}
			if login.challenge && (login.clientCalls != 0 || login.registered != 0) {
				t.Fatal("background challenge continued without verification")
			}
		})
	}
}

func TestPasswordlessRenewalSkipsDeviceRegistration(t *testing.T) {
	login := &fakeMaintainedLogin{account: "same"}
	got, _, err := renewSessionMode(func() (maintainedLogin, error) { return login, nil }, "same", appleservices.DeviceRegistration{}, false)
	if err != nil || got != login {
		t.Fatalf("passwordless renewal failed: %v", err)
	}
	if login.registered != 0 {
		t.Fatal("passwordless renewal attempted device registration")
	}
}

func TestDeviceListFailureExplainsIdentityRefresh(t *testing.T) {
	for _, err := range []error{
		appleservices.ErrDeviceIdentityRefreshRequired,
		&gsa.DeviceRequestError{HTTPStatus: 401},
	} {
		result := deviceListFailure(err)
		if result.Stage != "device_identity_refresh_required" || result.Message == "" {
			t.Fatalf("identity refresh was hidden as a generic failure: %#v", result)
		}
	}
	if result := deviceListFailure(errors.New("temporary failure")); result.Stage != "device_query_failed" {
		t.Fatalf("unrelated failure was misclassified: %#v", result)
	}
}

func TestReservedAliasPresentRequiresOneExactActiveMatch(t *testing.T) {
	alias := icloud.HideMyEmailAlias{Email: "Candidate@icloud.com", Label: "shop", Note: "order", Active: true}
	if !reservedAliasPresent(&icloud.HideMyEmailInfo{Aliases: []icloud.HideMyEmailAlias{alias}}, "candidate@icloud.com", "shop", "order") {
		t.Fatal("exact reserved alias was not reconciled")
	}
	for _, info := range []*icloud.HideMyEmailInfo{
		nil,
		{Aliases: []icloud.HideMyEmailAlias{{Email: alias.Email, Label: alias.Label, Note: alias.Note, Active: false}}},
		{Aliases: []icloud.HideMyEmailAlias{alias, alias}},
	} {
		if reservedAliasPresent(info, "candidate@icloud.com", "shop", "order") {
			t.Fatal("uncertain reserved alias was accepted")
		}
	}
}

func TestRuntimeAndStopAreBoundToProcess(t *testing.T) {
	dir := t.TempDir()
	status := runtimeStatus{PID: 123, Running: true, Authenticated: true, State: "online"}
	status.setDevices([]appleservices.AccountDevice{{Name: "test-installation"}}, "test-installation")
	for i := 0; i < 2; i++ {
		if err := status.save(dir); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var loaded runtimeStatus
	if json.Unmarshal(data, &loaded) != nil || !loaded.InstallationVisible || loaded.DeviceCount != 1 {
		t.Fatal("runtime record invalid")
	}
	if err := writeJSONFile(filepath.Join(dir, "stop-request.json"), map[string]int{"pid": 456}); err != nil {
		t.Fatal(err)
	}
	if stopRequested(dir, 123) {
		t.Fatal("stale request stopped a different process")
	}
	if err := writeJSONFile(filepath.Join(dir, "stop-request.json"), map[string]int{"pid": 123}); err != nil {
		t.Fatal(err)
	}
	if !stopRequested(dir, 123) || stopRequested(dir, 123) {
		t.Fatal("matching stop was not consumed exactly once")
	}
}
