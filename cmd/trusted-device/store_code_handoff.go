package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const storeCodeHandoffName = "store-code.handoff"

func storeCodeHandoffPath(dir string) string {
	return filepath.Join(dir, storeCodeHandoffName)
}

// waitForFreshStoreCode is entered only after Apple has returned its Store
// two-factor challenge. A code written before that point is discarded so it
// can never be replayed against a later authentication attempt.
func waitForFreshStoreCode(ctx context.Context, dir string) (string, error) {
	path := storeCodeHandoffPath(dir)
	_ = os.Remove(path)
	challengeAt := time.Now()
	fmt.Println("Apple Store 2FA challenge is active; awaiting a fresh private code handoff")

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = os.Remove(path)
			return "", errors.New("Store verification code wait expired")
		case <-ticker.C:
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() {
				_ = os.Remove(path)
				return "", errors.New("private Store verification handoff is invalid")
			}
			// Allow one second for coarse filesystem timestamps while still
			// rejecting files unrelated to this live challenge.
			if info.ModTime().Before(challengeAt.Add(-time.Second)) || time.Since(info.ModTime()) > 2*time.Minute {
				_ = os.Remove(path)
				continue
			}
			data, err := os.ReadFile(path)
			_ = os.Remove(path)
			if err != nil || len(data) > 32 {
				return "", errors.New("cannot read private Store verification handoff")
			}
			code := strings.TrimSpace(string(data))
			for _, r := range code {
				if r < '0' || r > '9' {
					code = ""
					break
				}
			}
			if len(code) != 6 {
				return "", errors.New("Store verification handoff must contain exactly six digits")
			}
			fmt.Println("Fresh Store verification code submitted to the retained challenge")
			return code, nil
		}
	}
}
