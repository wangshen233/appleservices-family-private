package main

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices"
)

func TestHelpAndInputValidationDoNotAuthenticate(t *testing.T) {
	t.Setenv("APPLESERVICES_APPLEID", "")
	t.Setenv("APPLESERVICES_PASSWORD", "")
	var output bytes.Buffer
	if err := run([]string{"-help"}, strings.NewReader(""), &output, &output); err != flag.ErrHelp {
		t.Fatalf("help returned %v", err)
	}
	for _, args := range [][]string{nil, {"-twofactor", "unknown"}, {"-dir", ""}, {"unexpected"}} {
		if err := run(args, strings.NewReader(""), &output, &output); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
}

func TestFileSessionRoundTripAndLogout(t *testing.T) {
	s := fileStore{dir: t.TempDir()}
	sess := &appleservices.Session{AppleID: "owner@example.test", DSID: "test-dsid", Cookies: []appleservices.Cookie{
		{URL: "https://gsa.apple.com/auth", Name: "test-cookie", Value: "test-value"},
	}}
	if err := s.SaveSession(sess); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AppleID != sess.AppleID || len(loaded.Cookies) != 1 {
		t.Fatal("lost account binding or cookies")
	}
	if err := s.SaveDevice(&appleservices.Device{Identifier: make([]byte, 16)}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"-dir", s.dir, "-logout"}, strings.NewReader(""), &output, &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.sessionPath()); !os.IsNotExist(err) {
		t.Fatal("logout did not remove local session")
	}
	if _, err := os.Stat(s.devicePath()); err != nil {
		t.Fatal("logout removed device identity")
	}
	if strings.Contains(output.String(), "test-value") {
		t.Fatal("printed session credential")
	}
}

func TestHelpDoesNotExposeEnvironmentCredentials(t *testing.T) {
	t.Setenv("APPLESERVICES_APPLEID", "private@example.test")
	t.Setenv("APPLESERVICES_PASSWORD", "test-private-password")
	var output bytes.Buffer
	if err := run([]string{"-help"}, strings.NewReader(""), &output, &output); err != flag.ErrHelp {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private@example.test") || strings.Contains(output.String(), "test-private-password") {
		t.Fatal("help exposed environment credentials")
	}
}
