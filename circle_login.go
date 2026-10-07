package appleservices

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/internal/circle"
	"math/big"
	"strconv"
)

// CircleVerification runs the requesting side entirely through GSA and APS.
// It never starts, drives, or modifies the official iCloud application.
type CircleVerification struct {
	login                *Login
	client               *circle.Client
	transaction, account string
	pushToken            []byte
	challenge            *aps.Event
	codeVerified         bool
}

func (l *Login) BeginCircleVerification(pushToken []byte) (*CircleVerification, error) {
	if !l.needsTwoFactor || l.result == nil {
		return nil, errors.New("appleservices: Circle verification requires a pending two-factor challenge")
	}
	if len(pushToken) != 32 {
		return nil, errors.New("appleservices: Circle verification requires a real APS token")
	}
	dsid := numericIdentity(l.result.SessionPayload)
	numericID, idErr := strconv.ParseUint(dsid, 10, 64)
	if idErr != nil || numericID == 0 || strconv.FormatUint(numericID, 10) != dsid {
		return nil, errors.New("appleservices: Circle verification challenge has no numeric identity")
	}
	// Native WinPiggy uses a decimal transaction followed by the attempt count.
	// The DER request UUID is a separate random 16-byte field.
	number, err := rand.Int(rand.Reader, big.NewInt(1<<31))
	if err != nil {
		return nil, err
	}
	transaction := strconv.FormatInt(number.Int64(), 10) + "-0"
	client, initial, err := circle.NewClient(l.adsid, transaction, dsid)
	if err != nil {
		return nil, err
	}
	verification := &CircleVerification{login: l, client: client, transaction: transaction, account: l.adsid, pushToken: append([]byte(nil), pushToken...)}
	if err = verification.send("", initial); err != nil {
		return nil, err
	}
	return verification, nil
}

func numericIdentity(payload map[string]any) string {
	// Apple has used all of these spellings across SPD revisions. Only accept
	// a canonical positive decimal value; never reinterpret an email or token.
	for _, key := range []string{"DsPrsId", "dsPrsId", "dsid", "prsId"} {
		switch v := payload[key].(type) {
		case uint64:
			if v > 0 {
				return strconv.FormatUint(v, 10)
			}
		case int64:
			if v > 0 {
				return strconv.FormatInt(v, 10)
			}
		case uint32:
			if v > 0 {
				return strconv.FormatUint(uint64(v), 10)
			}
		case int:
			if v > 0 {
				return strconv.Itoa(v)
			}
		case string:
			if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 && strconv.FormatUint(n, 10) == v {
				return v
			}
		}
	}
	return ""
}

func (v *CircleVerification) CorrelationID() string {
	sum := sha256.Sum256([]byte(v.transaction))
	return fmt.Sprintf("%x", sum[:16])
}

func (v *CircleVerification) send(idms string, response circle.Response) error {
	sender, ok := v.login.backend.(interface{ InitiateCircle(gsa.CircleReply) error })
	if !ok {
		return errors.New("appleservices: backend does not implement Circle initiation")
	}
	return sender.InitiateCircle(gsa.CircleReply{Transaction: v.transaction, IDMSData: idms, Step: response.Step, PAKE: response.PAKE, PushToken: v.pushToken, ErrorCode: response.ErrorCode})
}

// Matches permits a listener to route only this locally initiated transaction.
func (v *CircleVerification) Matches(event aps.Event) bool {
	return event.Command == 700 && event.CircleTransactionID == v.transaction && (event.AccountID == "" || event.AccountID == v.account)
}

// Receive handles server messages. At step 2 it records the challenge and asks
// for the code from the approved responder. At step 4 it verifies both proofs,
// decrypts the ADI code, submits it to Apple, and completes the iCloud delegate
// exchange. Successful completion therefore requires Apple's actual acceptance.
func (v *CircleVerification) Receive(event aps.Event) (bool, error) {
	if !v.Matches(event) {
		return false, errors.New("appleservices: Circle message belongs to another transaction")
	}
	if event.ErrorCode != nil && *event.ErrorCode != 0 && *event.ErrorCode != -9005 {
		return false, errors.New("appleservices: Circle peer rejected the request")
	}
	switch event.CircleStep {
	case 2:
		copy := event
		v.challenge = &copy
		return false, nil
	case 4:
		if v.codeVerified {
			return true, nil
		}
		message := circle.Message{AccountID: v.account, Transaction: v.transaction, IDMSData: event.IDMSData, Step: event.CircleStep, PAKE: event.PAKE, ErrorCode: event.ErrorCode}
		code, err := v.client.Receive(message)
		if err != nil {
			return false, err
		}
		if err = v.login.SubmitCode(TrustedDevice, code); err != nil {
			return false, err
		}
		if _, err = v.login.Client(); err != nil {
			return false, err
		}
		v.codeVerified = true
		return true, nil
	default:
		return false, errors.New("appleservices: unsupported Circle verification step")
	}
}

func (v *CircleVerification) SubmitPairingCode(code string) error {
	if v.challenge == nil {
		return errors.New("appleservices: waiting for the Circle challenge")
	}
	e := v.challenge
	response, err := v.client.Prove(circle.Message{AccountID: v.account, Transaction: v.transaction, IDMSData: e.IDMSData, Step: e.CircleStep, PAKE: e.PAKE, ErrorCode: e.ErrorCode}, code)
	if err != nil {
		return err
	}
	if err = v.send(e.IDMSData, response); err != nil {
		return err
	}
	return v.client.ProofSent()
}
