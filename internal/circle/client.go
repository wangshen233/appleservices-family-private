package circle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"math/big"
	"time"
)

// Client implements the initiating side without any official client window.
// Caller must authenticate to GSA first and bind incoming APS events to account
// and transaction. This object has no Apple credentials or network authority.
type Client struct {
	account, transaction, dsid string
	created                    time.Time
	a, A                       *big.Int
	key, m2                    []byte
	phase                      int
	pending                    *Response
	incoming                   [32]byte
	verifiedCode               string
}

func NewClient(account, transaction, dsid string) (*Client, Response, error) {
	if account == "" || transaction == "" || len(transaction) > 512 || dsid == "" {
		return nil, Response{}, errors.New("circle: incomplete client identity")
	}
	n, _ := new(big.Int).SetString(group3072, 16)
	secret := make([]byte, 32)
	uuid := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, Response{}, err
	}
	if _, err := rand.Read(uuid); err != nil {
		return nil, Response{}, err
	}
	a := new(big.Int).SetBytes(secret)
	if a.Sign() == 0 {
		return nil, Response{}, errors.New("circle: empty client secret")
	}
	A := new(big.Int).Exp(big.NewInt(5), a, n)
	initial, err := asn1.Marshal(step0{0, A.Bytes(), 1, uuid, []byte("o")})
	if err != nil {
		return nil, Response{}, err
	}
	c := &Client{account: account, transaction: transaction, dsid: dsid, created: time.Now(), a: a, A: A, phase: 2}
	return c, Response{Step: 0, PAKE: base64.StdEncoding.EncodeToString(initial)}, nil
}

// Prove returns the client proof for the manually approved pairing code.
func (c *Client) Prove(m Message, code string) (Response, error) {
	if err := c.bind(m, 2); err != nil {
		return Response{}, err
	}
	if !codeFormat.MatchString(code) {
		return Response{}, errors.New("circle: six-digit pairing code required")
	}
	data, err := payload(m)
	if err != nil {
		return Response{}, err
	}
	digest := sha256.Sum256(data)
	if c.pending != nil && digest == c.incoming {
		return *c.pending, nil
	}
	var challenge step1
	if err = decode(data, &challenge); err != nil {
		return Response{}, err
	}
	if challenge.Step != 1 {
		return Response{}, errors.New("circle: invalid server challenge step")
	}
	var body step1Body
	if err = decode(challenge.Body, &body); err != nil {
		return Response{}, err
	}
	if len(body.Salt) != 16 || len(body.Public) > 384 {
		return Response{}, errors.New("circle: invalid server challenge parameters")
	}
	n, _ := new(big.Int).SetString(group3072, 16)
	g := big.NewInt(5)
	B := new(big.Int).SetBytes(body.Public)
	if B.Sign() == 0 || B.Cmp(n) >= 0 {
		return Response{}, errors.New("circle: invalid server public value")
	}
	paddedG := make([]byte, 384)
	paddedG[383] = 5
	k := new(big.Int).SetBytes(hash(n.Bytes(), paddedG))
	u := new(big.Int).SetBytes(hash(c.A.Bytes(), B.Bytes()))
	if u.Sign() == 0 {
		return Response{}, errors.New("circle: invalid scrambling parameter")
	}
	x := new(big.Int).SetBytes(hash(body.Salt, hash([]byte(c.dsid+":"+code))))
	base := new(big.Int).Mod(new(big.Int).Sub(B, new(big.Int).Mul(k, new(big.Int).Exp(g, x, n))), n)
	exponent := new(big.Int).Add(c.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exponent, n)
	key := hash(S.Bytes())
	xor := hash(n.Bytes())
	hg := hash(paddedG)
	for i := range xor {
		xor[i] ^= hg[i]
	}
	proof := hash(xor, hash([]byte(c.dsid)), body.Salt, c.A.Bytes(), B.Bytes(), key)
	c.key = key
	c.m2 = hash(c.A.Bytes(), proof, key)
	encoded, err := asn1.Marshal(step2{2, proof})
	if err != nil {
		return Response{}, err
	}
	response := Response{Step: 2, PAKE: base64.StdEncoding.EncodeToString(encoded)}
	c.pending = &response
	c.incoming = digest
	return response, nil
}

func (c *Client) ProofSent() error {
	if c.phase != 2 || c.pending == nil {
		return errors.New("circle: proof not prepared")
	}
	c.phase = 4
	return nil
}

// Receive verifies the SERVER proof before decrypting a verification code.
func (c *Client) Receive(m Message) (string, error) {
	if err := c.bind(m, 4); err != nil {
		return "", err
	}
	data, err := payload(m)
	if err != nil {
		return "", err
	}
	var response step3
	if err = decode(data, &response); err != nil {
		return "", err
	}
	if response.Step != 3 || !hmac.Equal(response.Proof, c.m2) {
		return "", errors.New("circle: server proof rejected")
	}
	var message sealed
	if err = decode(response.Payload, &message); err != nil {
		return "", err
	}
	if len(message.Nonce) != 16 || len(message.Tag) != 16 || len(message.Ciphertext) > 256 {
		return "", errors.New("circle: invalid encrypted code envelope")
	}
	key, err := hkdf.Key(sha256.New, c.key, nil, "recv->send", 16)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCMWithNonceSize(block, 16)
	plaintext, err := aead.Open(nil, message.Nonce, append(message.Ciphertext, message.Tag...), nil)
	if err != nil {
		return "", errors.New("circle: encrypted code authentication failed")
	}
	var value asn1.RawValue
	if err = decode(plaintext, &value); err != nil {
		return "", err
	}
	code := string(value.Bytes)
	if value.Class != 0 || value.Tag != 12 || !codeFormat.MatchString(code) {
		return "", errors.New("circle: invalid decrypted verification code")
	}
	c.verifiedCode = code
	return code, nil
}

func (c *Client) bind(m Message, step int) error {
	if time.Since(c.created) > 2*time.Minute {
		return errors.New("circle: client exchange expired")
	}
	if m.AccountID != c.account || m.Transaction != c.transaction || m.Step != step || c.phase != step {
		return errors.New("circle: client request binding mismatch")
	}
	if m.ErrorCode != nil && *m.ErrorCode != 0 && *m.ErrorCode != -9005 {
		return errors.New("circle: peer rejected client exchange")
	}
	return nil
}
