package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/aps"
)

type fakePush struct {
	token   []byte
	failure error
	closed  bool
}

func (p *fakePush) Connect(context.Context) error                       { return p.failure }
func (p *fakePush) State() aps.State                                    { return aps.State{Token: p.token} }
func (p *fakePush) Listen(context.Context, func(aps.Event) error) error { panic("unused") }
func (p *fakePush) Close() error                                        { p.closed = true; return nil }

func TestPushRecoveryDoesNotAdoptUnboundToken(t *testing.T) {
	for _, test := range []struct {
		name      string
		candidate fakePush
		expected  error
	}{
		{"same-token", fakePush{token: []byte("existing-token")}, nil},
		{"changed-token", fakePush{token: []byte("different-token")}, errPushTokenChanged},
		{"connection-failed", fakePush{failure: context.DeadlineExceeded}, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := test.candidate
			connected, err := reconnectPush(context.Background(), []byte("existing-token"), func() (pushTransport, error) { return &candidate, nil })
			if !errors.Is(err, test.expected) {
				t.Fatalf("unexpected recovery result: %v", err)
			}
			if test.expected != nil && (connected != nil || !candidate.closed) {
				t.Fatal("failed or unbound connection adopted")
			}
			if test.expected == nil && (connected != &candidate || candidate.closed) {
				t.Fatal("valid connection was not retained")
			}
		})
	}
}

func TestPushBackoffIsBounded(t *testing.T) {
	delay := time.Duration(0)
	for i := 0; i < 20; i++ {
		delay = nextPushDelay(delay)
		if delay < 2*time.Second || delay > time.Minute {
			t.Fatal("retry delay outside bounds")
		}
	}
	if delay != time.Minute {
		t.Fatal("retry delay did not reach cap")
	}
}
