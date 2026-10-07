package main

import (
	"context"
	"errors"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/internal/control"
)

// completeCircleLogin reuses the worker's existing APS listener. It does not
// subscribe a second client or control the official application.
func completeCircleLogin(ctx context.Context, login *appleservices.Login, token []byte, events <-chan aps.Event, failures <-chan error, commands <-chan control.Command, status *runtimeStatus, dir string) error {
	verification, err := login.BeginCircleVerification(token)
	if err != nil {
		return err
	}
	status.State, status.VerificationRequired = "awaiting_pairing", true
	if err = status.save(dir); err != nil {
		return err
	}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Circle verification timed out; no Apple acceptance received")
		case <-failures:
			return errors.New("APS connection ended during Circle verification")
		case event := <-events:
			if !verification.Matches(event) {
				continue
			}
			complete, err := verification.Receive(event)
			if err != nil {
				return err
			}
			if complete {
				status.State, status.VerificationRequired = "signing_in", false
				return status.save(dir)
			}
			status.State = "awaiting_pairing_code"
			if err = status.save(dir); err != nil {
				return err
			}
		case command := <-commands:
			if command.Context.Err() != nil {
				continue
			}
			result := control.Result{Message: "请先完成已授权设备的配对验证"}
			if command.Action.Action == "submit-pairing" {
				if err = verification.SubmitPairingCode(command.Code); err != nil {
					result.Message = "配对证明未发送，检查验证码或等待设备响应"
				} else {
					result.OK, result.Message = true, "配对证明已发送，等待 Apple 验证结果"
					status.State = "verifying_pairing"
					if err = status.save(dir); err != nil {
						return err
					}
				}
			}
			command.Reply <- result
		}
	}
}
