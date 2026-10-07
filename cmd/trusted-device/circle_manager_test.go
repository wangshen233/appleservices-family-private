package main

import (
	"testing"

	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/internal/circle"
)

func TestCircleManagerKeepsDuplicateInitialRequestBound(t *testing.T) {
	client, initial, err := circle.NewClient("account", "transaction", "12345")
	if err != nil {
		t.Fatal(err)
	}
	event := aps.Event{Command: 700, AccountID: "account", TransactionID: "signin", CircleTransactionID: "circle", CircleStep: 1, IDMSData: "opaque", PAKE: initial.PAKE}
	m := newCircleManager("account", "12345")
	if err = m.remember(event); err != nil {
		t.Fatal(err)
	}
	if err = m.remember(event); err != nil {
		t.Fatal(err)
	}
	if m.pending("signin") == nil {
		t.Fatal("duplicate initial request lost pending session")
	}
	if _, err = m.approve("signin", func(_ aps.Event, response circle.Response) error {
		if response.Step != 1 || response.PAKE == "" {
			t.Fatal("invalid initial responder message")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if m.pending("signin") != nil {
		t.Fatal("approved session still reported pending")
	}
	_ = client
}
