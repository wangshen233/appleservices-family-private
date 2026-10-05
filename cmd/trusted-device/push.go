package main

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/Laky-64/appleservices/aps"
)

type pushTransport interface {
	Connect(context.Context) error
	State() aps.State
	Listen(context.Context, func(aps.Event) error) error
	Close() error
}

var errPushTokenChanged = errors.New("push token changed; restart with the saved device identity to rebind registration")

type pushConnection struct {
	client pushTransport
	err    error
}

// Reconnect only adopts the token already bound to ADI and the account. A new
// token requires a complete startup with the saved identity, never a silent
// replacement while headers and registration still refer to the old token.
func reconnectPush(ctx context.Context, token []byte, factory func() (pushTransport, error)) (pushTransport, error) {
	candidate, err := factory()
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	err = candidate.Connect(connectCtx)
	cancel()
	if err == nil && !bytes.Equal(candidate.State().Token, token) {
		err = errPushTokenChanged
	}
	if err != nil {
		candidate.Close()
		return nil, err
	}
	return candidate, nil
}

func nextPushDelay(delay time.Duration) time.Duration {
	if delay < 2*time.Second {
		return 2 * time.Second
	}
	if delay >= 30*time.Second {
		return time.Minute
	}
	return delay * 2
}
