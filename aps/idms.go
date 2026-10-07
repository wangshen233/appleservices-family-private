package aps

import (
	"encoding/json"
	"errors"
)

const IDMSAuthTopic = "com.apple.idmsauth"

var ErrUnhandledNotice = errors.New("aps: unsupported IDMS notice")

type Event struct {
	Command               int    `json:"cmd"`
	TransactionID         string `json:"txnid"`
	PreviousTransactionID string `json:"prevtxnid"`
	AccountID             string `json:"adsid"`
	CircleTransactionID   string `json:"atxnid,omitempty"`
	CircleStep            int    `json:"step,omitempty"`
	PAKE                  string `json:"pake,omitempty"`
	IDMSData              string `json:"idmsdata,omitempty"`
	ErrorCode             *int   `json:"ec,omitempty"`
	// Apple uses both string and object forms of aps.alert. This client does
	// not display that text; keep it opaque so its shape cannot break routing.
	Alert json.RawMessage `json:"aps,omitempty"`
	// Unsupported marks a notification shape that this client does not yet
	// understand. Keep only size metadata; never log the opaque payload.
	Unsupported bool `json:"-"`
	NoticeSize  int  `json:"-"`
}

// ParseEvent decodes AuthKit notifications. Command 100 requests approval;
// 400 cancels a request. Neither notification guarantees a plaintext OTP.
func ParseEvent(payload []byte) (Event, error) {
	var event Event
	if len(payload) > maxFrame {
		return Event{Unsupported: true, NoticeSize: len(payload)}, errors.New("aps: IDMS payload too large")
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return Event{Unsupported: true, NoticeSize: len(payload)}, errors.New("aps: invalid IDMS JSON")
	}
	switch event.Command {
	case 100:
		if event.AccountID == "" || event.TransactionID == "" {
			return Event{}, errors.New("aps: sign-in notification missing account or transaction")
		}
	case 400:
		if event.PreviousTransactionID == "" {
			return Event{}, errors.New("aps: cancellation missing transaction")
		}
	case 1200:
		if event.AccountID == "" {
			return Event{}, errors.New("aps: device-status notification missing account")
		}
	case 700:
		// A pairing request is not an authentication or transport failure.
		// Deliver it for an explicit capability notice without attempting pairing.
	default:
		event.Unsupported = true
		event.NoticeSize = len(payload)
		return event, ErrUnhandledNotice
	}
	return event, nil
}
