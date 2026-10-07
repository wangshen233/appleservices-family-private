package main

import (
	"errors"
	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/internal/circle"
)

type circleRecord struct {
	session  *circle.Session
	initial  aps.Event
	approved bool
}
type circleManager struct {
	records       map[string]*circleRecord
	account, dsid string
}

func newCircleManager(account, dsid string) *circleManager {
	return &circleManager{records: map[string]*circleRecord{}, account: account, dsid: dsid}
}
func circleMessage(e aps.Event) circle.Message {
	return circle.Message{AccountID: e.AccountID, Transaction: e.CircleTransactionID, IDMSData: e.IDMSData, Step: e.CircleStep, PAKE: e.PAKE, ErrorCode: e.ErrorCode}
}
func (m *circleManager) expire() {
	for id, r := range m.records {
		if r.session.Expired() {
			delete(m.records, id)
		}
	}
}
func (m *circleManager) remember(e aps.Event) error {
	m.expire()
	if e.AccountID != m.account || e.TransactionID == "" || len(e.TransactionID) > 512 {
		return errors.New("circle: initial notification is not bound to this account and login")
	}
	if old := m.records[e.CircleTransactionID]; old != nil {
		if old.initial.TransactionID != e.TransactionID || old.initial.PAKE != e.PAKE || old.initial.IDMSData != e.IDMSData {
			return errors.New("circle: initial notification changed")
		}
		return nil
	}
	if len(m.records) >= 5 {
		return errors.New("circle: too many pending sessions")
	}
	s, err := circle.New(circleMessage(e), m.dsid)
	if err != nil {
		return err
	}
	m.records[e.CircleTransactionID] = &circleRecord{session: s, initial: e}
	return nil
}
func (m *circleManager) pending(txn string) *circleRecord {
	m.expire()
	for _, r := range m.records {
		if r.initial.TransactionID == txn && !r.approved {
			return r
		}
	}
	return nil
}
func (m *circleManager) cancel(txn string) {
	for id, r := range m.records {
		if r.initial.TransactionID == txn {
			delete(m.records, id)
		}
	}
}

type circleSender func(aps.Event, circle.Response) error

func (m *circleManager) approve(txn string, send circleSender) (string, error) {
	r := m.pending(txn)
	if r == nil {
		return "", errors.New("circle: pending session no longer exists")
	}
	response := r.session.InitialResponse()
	if err := send(r.initial, response); err != nil {
		return "", err
	}
	if err := r.session.Commit(response.Step); err != nil {
		return "", err
	}
	r.approved = true
	return r.session.Code(), nil
}
func (m *circleManager) handle(e aps.Event, send circleSender, code func() (string, error)) (string, error) {
	m.expire()
	r := m.records[e.CircleTransactionID]
	if r == nil || !r.approved {
		return "", errors.New("circle: no approved session for this message")
	}
	if e.AccountID != "" && e.AccountID != m.account {
		return "", errors.New("circle: notification account mismatch")
	}
	// Follow-up Circle notices need not repeat adsid. Bind them to the already
	// approved transaction; never infer an account for an unsolicited first step.
	if e.AccountID == "" {
		e.AccountID = m.account
	}
	response, err := r.session.Prepare(circleMessage(e), code)
	if err != nil {
		return "", err
	}
	if err = send(e, response); err != nil {
		return "", err
	}
	if err = r.session.Commit(response.Step); err != nil {
		return "", err
	}
	if e.CircleStep == 5 {
		return "exchange_complete_no_voucher", nil
	}
	return "code_exchanged", nil
}
