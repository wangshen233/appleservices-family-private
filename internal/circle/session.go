package circle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"time"
)

var codeFormat = regexp.MustCompile(`^[0-9]{6}$`)

type Message struct {
	AccountID   string
	Transaction string
	IDMSData    string
	Step        int
	PAKE        string
	ErrorCode   *int
}

type Response struct {
	Step      int
	PAKE      string
	ErrorCode *int
}

// Session is memory-only, bound to one account, transaction and initial request.
// The caller sends the response successfully before Commit advances the state.
type Session struct {
	account, transaction, idms string
	created                    time.Time
	srp                        *srpServer
	otp                        string
	phase                      int
	requestDigest              [32]byte
	response                   *Response
}

type step0 struct {
	Step    int
	Public  []byte
	Version int
	UUID    []byte
	Tag     []byte
}
type step1 struct {
	Step int
	Body []byte
}
type step1Body struct {
	Salt   []byte
	Public []byte
}
type step2 struct {
	Step  int
	Proof []byte
}
type step3 struct {
	Step    int
	Proof   []byte
	Payload []byte
}
type step4 struct {
	Step    int
	Welcome []byte
	Extra   []byte `asn1:"optional"`
}
type step5 struct {
	Step    int
	Voucher []byte
}
type sealed struct{ Nonce, Ciphertext, Tag []byte }

func decode(data []byte, value any) error {
	rest, err := asn1.Unmarshal(data, value)
	if err != nil || len(rest) != 0 {
		return errors.New("circle: invalid DER message")
	}
	return nil
}

func payload(m Message) ([]byte, error) {
	if len(m.PAKE) > 16384 {
		return nil, errors.New("circle: oversized PAKE message")
	}
	b, err := base64.StdEncoding.DecodeString(m.PAKE)
	if err != nil {
		return nil, errors.New("circle: invalid PAKE encoding")
	}
	return b, nil
}

func New(m Message, dsid string) (*Session, error) {
	if m.AccountID == "" || m.Transaction == "" || len(m.Transaction) > 512 || m.IDMSData == "" || len(m.IDMSData) > 16384 || m.Step != 1 {
		return nil, errors.New("circle: incomplete initial request")
	}
	if m.ErrorCode != nil && *m.ErrorCode != 0 {
		return nil, errors.New("circle: initial request has an error")
	}
	if _, err := strconv.ParseUint(dsid, 10, 64); err != nil {
		return nil, errors.New("circle: numeric account identity required")
	}
	data, err := payload(m)
	if err != nil {
		return nil, err
	}
	var incoming step0
	if err = decode(data, &incoming); err != nil {
		return nil, err
	}
	if incoming.Step != 0 || incoming.Version != 1 || len(incoming.UUID) != 16 || string(incoming.Tag) != "o" {
		return nil, errors.New("circle: unsupported initial PAKE parameters")
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return nil, err
	}
	otp := fmt.Sprintf("%06d", number.Int64())
	srp, err := newSRP(dsid, otp, incoming.Public)
	if err != nil {
		return nil, err
	}
	body, err := asn1.Marshal(step1Body{srp.salt, srp.public})
	if err != nil {
		return nil, err
	}
	encoded, err := asn1.Marshal(step1{1, body})
	if err != nil {
		return nil, err
	}
	return &Session{account: m.AccountID, transaction: m.Transaction, idms: m.IDMSData, created: time.Now(), srp: srp, otp: otp, phase: 1, requestDigest: sha256.Sum256(data), response: &Response{Step: 1, PAKE: base64.StdEncoding.EncodeToString(encoded)}}, nil
}

func (s *Session) Code() string              { return s.otp }
func (s *Session) InitialResponse() Response { return *s.response }
func (s *Session) Expired() bool             { return time.Since(s.created) > 2*time.Minute }

func (s *Session) Prepare(m Message, generateCode func() (string, error)) (Response, error) {
	if s.Expired() {
		return Response{}, errors.New("circle: request expired")
	}
	if m.AccountID != s.account || m.Transaction != s.transaction || m.IDMSData != s.idms {
		return Response{}, errors.New("circle: request binding mismatch")
	}
	if m.ErrorCode != nil && *m.ErrorCode != 0 {
		return Response{}, errors.New("circle: peer reported an error")
	}
	data, err := payload(m)
	if err != nil {
		return Response{}, err
	}
	digest := sha256.Sum256(data)
	if s.response != nil && digest == s.requestDigest && m.Step == s.response.Step {
		return *s.response, nil
	}
	if m.Step != s.phase {
		return Response{}, errors.New("circle: unexpected step")
	}
	var encoded []byte
	switch m.Step {
	case 3:
		var proof step2
		if err = decode(data, &proof); err != nil {
			return Response{}, err
		}
		if proof.Step != 2 || !s.srp.verify(proof.Proof) {
			return Response{}, errors.New("circle: client proof rejected")
		}
		code, err := generateCode()
		if err != nil {
			return Response{}, errors.New("circle: verification code unavailable")
		}
		if !codeFormat.MatchString(code) {
			return Response{}, errors.New("circle: invalid verification code")
		}
		// Rust's DER String is UTF8String, not Go's default PrintableString.
		codeDER, _ := asn1.Marshal(asn1.RawValue{Class: 0, Tag: 12, Bytes: []byte(code)})
		secret, err := s.encrypt(codeDER, "recv->send")
		if err != nil {
			return Response{}, err
		}
		encoded, err = asn1.Marshal(step3{3, s.srp.m2, secret})
		if err != nil {
			return Response{}, err
		}
	case 5:
		var welcome step4
		if err = decode(data, &welcome); err != nil {
			return Response{}, err
		}
		if welcome.Step != 4 || len(welcome.Welcome) > 8192 {
			return Response{}, errors.New("circle: invalid final request")
		}
		// This installation has no Octagon peer keys. Report an empty voucher
		// truthfully; never manufacture a signature or claim encrypted trust.
		encoded, err = asn1.Marshal(step5{5, []byte{}})
		if err != nil {
			return Response{}, err
		}
	default:
		return Response{}, errors.New("circle: unsupported step")
	}
	response := Response{Step: m.Step, PAKE: base64.StdEncoding.EncodeToString(encoded)}
	s.response = &response
	s.requestDigest = digest
	return response, nil
}

func (s *Session) Commit(step int) error {
	if s.response != nil && s.response.Step == step && s.phase == step+2 {
		return nil
	}
	if s.response == nil || s.response.Step != step || step != s.phase {
		return errors.New("circle: invalid response commit")
	}
	s.phase += 2
	return nil
}

func (s *Session) encrypt(data []byte, label string) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, s.srp.key, nil, label, 16)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, data, nil)
	offset := len(ciphertext) - 16
	return asn1.Marshal(sealed{nonce, ciphertext[:offset], ciphertext[offset:]})
}
