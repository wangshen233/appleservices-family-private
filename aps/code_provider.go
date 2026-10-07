package aps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Laky-64/appleservices/internal/backgroundexec"
)

// CodeRequest must describe the same anisette identity used for registration.
// The local provider receives no account password or iCloud service token.
type CodeRequest struct {
	DeviceID         string `json:"device_id"`
	Identifier       []byte `json:"identifier"`
	ProvisioningBlob []byte `json:"adi_pb"`
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.Len() {
		return 0, errors.New("aps: code provider output exceeds limit")
	}
	return w.Buffer.Write(data)
}

func ValidateCodeProvider(path string) error {
	if path == "" {
		return errors.New("aps: a working local ADI verification-code provider is required; this project does not yet include one")
	}
	if !filepath.IsAbs(path) {
		return errors.New("aps: code provider must be an absolute executable path")
	}
	resolved, err := exec.LookPath(path)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
		return errors.New("aps: local ADI code-provider executable is unavailable")
	}
	return nil
}

// CodeFromProcess delegates generation to an installed, working local ADI
// implementation. It does not invent a code or derive one from an APS token.
func CodeFromProcess(ctx context.Context, path string, request CodeRequest) (string, error) {
	if err := ValidateCodeProvider(path); err != nil {
		return "", err
	}
	if request.DeviceID == "" || len(request.Identifier) != 16 || len(request.ProvisioningBlob) == 0 {
		return "", errors.New("aps: code generation requires provisioned device identity")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, path)
	backgroundexec.Configure(command)
	command.Stdin = bytes.NewReader(data)
	output := &boundedOutput{limit: 4096}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return "", errors.New("aps: local ADI code generation failed")
	}
	var response struct {
		DeviceID string `json:"device_id"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return "", errors.New("aps: code provider returned invalid JSON")
	}
	if response.DeviceID != request.DeviceID {
		return "", errors.New("aps: code provider used a different device identity")
	}
	if len(response.Code) != 6 || strings.IndexFunc(response.Code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", errors.New("aps: code provider returned no six-digit code")
	}
	return response.Code, nil
}
