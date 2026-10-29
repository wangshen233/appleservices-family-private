package dashboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Laky-64/appleservices/nativeadi"
)

// ensureMacPushState establishes the account-private Mac APS certificate,
// key and token before the trusted-device worker starts. It never receives
// Apple account credentials and refuses to accept a partial result.
func (s *Server) ensureMacPushState(ctx context.Context, dir string) error {
	if macPushStateReady(dir) {
		return nil
	}
	if s.provisionMacPush == nil {
		return errors.New("Mac APS provisioner unavailable")
	}
	if err := s.provisionMacPush(ctx, dir); err != nil {
		return err
	}
	if !macPushStateReady(dir) {
		return errors.New("Mac APS provisioner returned incomplete state")
	}
	return nil
}

func macPushStateReady(dir string) bool {
	for _, path := range []string{
		filepath.Join(dir, "device.json"),
		filepath.Join(dir, "native-adi", "hw_info.plist"),
		filepath.Join(dir, "push.json"),
	} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func (s *Server) runMacPushProvisioner(ctx context.Context, dir string) error {
	if s.config.Python == "" || s.config.MacPushSetup == "" || s.config.Library == "" {
		return errors.New("Mac APS provisioner is not configured")
	}
	if _, err := os.Stat(s.config.MacPushSetup); err != nil {
		return errors.New("Mac APS provisioner is unavailable")
	}
	if err := nativeadi.ValidateLibrary(s.config.Library); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, s.config.Python, s.config.MacPushSetup,
		"--profile", filepath.Join(dir, "native-adi"),
		"--device", filepath.Join(dir, "device.json"),
		"--library", s.config.Library,
		"--go-state-out", filepath.Join(dir, "push.json"))
	configureProcess(command)
	// The bootstrap has no reason to receive credentials or proxy overrides.
	// This also keeps a direct Apple APS path when the desktop environment has
	// unrelated proxy variables configured.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(key, "APPLESERVICES_APPLEID"),
			strings.EqualFold(key, "APPLESERVICES_PASSWORD"),
			strings.EqualFold(key, "APPLESERVICES_STORE_CODE"),
			strings.EqualFold(key, "HTTP_PROXY"),
			strings.EqualFold(key, "HTTPS_PROXY"),
			strings.EqualFold(key, "ALL_PROXY"),
			strings.EqualFold(key, "APPLESERVICES_SOCKS_PROXY"):
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "NO_PROXY=*")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return errors.New("Mac APS bootstrap failed")
	}
	return nil
}
