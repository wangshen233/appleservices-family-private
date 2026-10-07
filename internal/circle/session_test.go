package circle

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func reference(t *testing.T) (map[string]string, *srpServer) {
	t.Helper()
	data, err := os.ReadFile("testdata/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err = json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	s, err := deriveSRP(v["identity"], v["password"], unhex(t, v["a_public"]), unhex(t, v["salt"]), unhex(t, v["b"]))
	if err != nil {
		t.Fatal(err)
	}
	return v, s
}
func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func encoded(t *testing.T, v any) string {
	t.Helper()
	b, e := asn1.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestIndependentClientReference(t *testing.T) {
	v, s := reference(t)
	for name, value := range map[string][]byte{"b_public": s.public, "key": s.key, "m1": s.m1, "m2": s.m2} {
		if !bytes.Equal(value, unhex(t, v[name])) {
			t.Fatalf("independent %s mismatch", name)
		}
	}
	key, err := hkdf.Key(sha256.New, s.key, nil, "recv->send", 16)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, unhex(t, v["aead_key"])) {
		t.Fatal("independent HKDF mismatch")
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCMWithNonceSize(block, 16)
	plaintext, err := gcm.Open(nil, unhex(t, v["nonce"]), unhex(t, v["encrypted"]), nil)
	if err != nil || !bytes.Equal(plaintext, unhex(t, v["plaintext"])) {
		t.Fatal("independent 16-byte nonce AEAD mismatch")
	}
}

func TestExchangeProofBindingRetriesAndCompletion(t *testing.T) {
	v, srp := reference(t)
	s := &Session{account: "account", transaction: "transaction", idms: "idms", created: time.Now(), srp: srp, phase: 3}
	m := Message{AccountID: "account", Transaction: "transaction", IDMSData: "idms", Step: 3, PAKE: encoded(t, step2{2, srp.m1})}
	calls := 0
	code := func() (string, error) { calls++; return "098765", nil }
	for _, change := range []func(*Message){func(m *Message) { m.AccountID = "other" }, func(m *Message) { m.Transaction = "other" }, func(m *Message) { m.IDMSData = "other" }, func(m *Message) { m.Step = 5 }, func(m *Message) { m.PAKE = encoded(t, step2{2, make([]byte, 32)}) }} {
		bad := m
		change(&bad)
		if _, err := s.Prepare(bad, code); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if calls != 0 {
		t.Fatal("code generated before proof verification")
	}
	reply, err := s.Prepare(m, code)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Prepare(m, code)
	if err != nil || reply.PAKE != retry.PAKE || calls != 1 {
		t.Fatal("transport retry must reuse ciphertext and code")
	}
	if err = s.Commit(3); err != nil {
		t.Fatal(err)
	}
	retry, err = s.Prepare(m, code)
	if err != nil || reply.PAKE != retry.PAKE || calls != 1 {
		t.Fatal("delivery replay must be idempotent")
	}
	if err = s.Commit(3); err != nil {
		t.Fatal("replay commit must be idempotent")
	}
	data, _ := base64.StdEncoding.DecodeString(reply.PAKE)
	var response step3
	if err = decode(data, &response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.Proof, unhex(t, v["m2"])) {
		t.Fatal("server proof mismatch")
	}
	var envelope sealed
	if err = decode(response.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(unhex(t, v["aead_key"]))
	gcm, _ := cipher.NewGCMWithNonceSize(block, 16)
	plaintext, err := gcm.Open(nil, envelope.Nonce, append(envelope.Ciphertext, envelope.Tag...), nil)
	if err != nil || !bytes.Equal(plaintext, unhex(t, v["plaintext"])) {
		t.Fatal("response is not interoperable UTF8String AEAD")
	}
	m.Step = 5
	m.PAKE = encoded(t, step4{Step: 4, Welcome: []byte{1}})
	last, err := s.Prepare(m, code)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = base64.StdEncoding.DecodeString(last.PAKE)
	var final step5
	if err = decode(data, &final); err != nil || final.Step != 5 || len(final.Voucher) != 0 {
		t.Fatal("must explicitly report absent voucher")
	}
	if err = s.Commit(5); err != nil {
		t.Fatal(err)
	}
	s.created = time.Now().Add(-3 * time.Minute)
	if _, err = s.Prepare(m, code); err == nil {
		t.Fatal("expired replay accepted")
	}
}

func TestInitialAndMalformedRequests(t *testing.T) {
	v, _ := reference(t)
	m := Message{AccountID: "account", Transaction: "transaction", IDMSData: "idms", Step: 1, PAKE: encoded(t, step0{0, unhex(t, v["a_public"]), 1, make([]byte, 16), []byte("o")})}
	s, err := New(m, v["identity"])
	if err != nil {
		t.Fatal(err)
	}
	if !codeFormat.MatchString(s.Code()) {
		t.Fatal("bad initial code")
	}
	if _, err = s.Prepare(Message{AccountID: m.AccountID, Transaction: m.Transaction, IDMSData: m.IDMSData, Step: 3, PAKE: encoded(t, step2{2, make([]byte, 32)})}, func() (string, error) { t.Fatal("unapproved code request"); return "", nil }); err == nil {
		t.Fatal("unapproved request accepted")
	}
	if err = s.Commit(3); err == nil {
		t.Fatal("unprepared response committed")
	}
	if err = s.Commit(1); err != nil {
		t.Fatal(err)
	}
	for _, pub := range [][]byte{{0}, make([]byte, 385), unhex(t, group3072)} {
		if _, err := deriveSRP("test", "012345", pub, make([]byte, 16), []byte{1}); err == nil {
			t.Fatal("invalid public value accepted")
		}
	}
}
