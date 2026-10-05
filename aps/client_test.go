package aps

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

func syntheticState(t *testing.T) State {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local protocol test only"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
	}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return State{
		CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}),
		PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
}

func TestClientAuthenticatesAndAcknowledgesAuthKitNotification(t *testing.T) {
	client, err := NewClient(syntheticState(t))
	if err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	client.connection = left
	defer client.Close()
	defer right.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	token := bytes.Repeat([]byte{42}, 32)
	errCh := make(chan error, 1)
	go func() {
		connect, e := readFrame(right)
		if e != nil {
			errCh <- e
			return
		}
		if connect.command != 7 || !bytes.Equal(connect.get(5), []byte{0, 0, 0, 65}) {
			errCh <- context.Canceled
			return
		}
		cert, e := x509.ParseCertificate(connect.get(12))
		if e != nil {
			errCh <- e
			return
		}
		signature := connect.get(14)
		digest := sha1.Sum(connect.get(13))
		if len(signature) < 2 || !bytes.Equal(signature[:2], []byte{1, 1}) {
			errCh <- context.Canceled
			return
		}
		e = rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA1, digest[:], signature[2:])
		if e != nil {
			errCh <- e
			return
		}
		e = writeFrame(right, frame{8, []field{{1, []byte{0}}, {3, token}}})
		if e != nil {
			errCh <- e
			return
		}
		state, e := readFrame(right)
		if e != nil {
			errCh <- e
			return
		}
		topics, e := readFrame(right)
		if e != nil {
			errCh <- e
			return
		}
		hash := sha1.Sum([]byte(IDMSAuthTopic))
		if state.command != 20 || topics.command != 9 || !bytes.Equal(topics.get(2), hash[:]) {
			errCh <- context.Canceled
			return
		}
		e = writeFrame(right, frame{10, []field{{1, token}, {2, hash[:]}, {3, []byte(`{"cmd":100,"adsid":"test-account","txnid":"test-transaction","aps":{"alert":{"body":"local fixture"}}}`)}, {4, []byte{0, 0, 0, 7}}}})
		if e != nil {
			errCh <- e
			return
		}
		ack, e := readFrame(right)
		if e == nil && (ack.command != 11 || !bytes.Equal(ack.get(4), []byte{0, 0, 0, 7}) || !bytes.Equal(ack.get(8), []byte{0})) {
			e = context.Canceled
		}
		errCh <- e
	}()
	if err := client.authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(client.State().Token, token) {
		t.Fatal("APS token was not preserved")
	}
	seen := false
	err = client.Listen(ctx, func(event Event) error {
		seen = event.Command == 100 && event.AccountID == "test-account" && event.TransactionID == "test-transaction"
		return context.Canceled
	})
	if err != context.Canceled || !seen {
		t.Fatalf("unexpected listener result: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsMismatchedCertificateAndKey(t *testing.T) {
	state := syntheticState(t)
	other := syntheticState(t)
	state.PrivateKeyPEM = other.PrivateKeyPEM
	if _, err := NewClient(state); err == nil {
		t.Fatal("accepted mismatched push credentials")
	}
}

func TestMalformedFramesAndIncompleteEventsFail(t *testing.T) {
	for _, data := range [][]byte{{10, 0, 0, 0, 1, 1}, {10, 0, 0, 0, 4, 1, 0, 4, 0}, {10, 0xff, 0xff, 0xff, 0xff}} {
		if _, err := readFrame(bytes.NewReader(data)); err == nil {
			t.Fatal("accepted a malformed frame")
		}
	}
	for _, payload := range []string{`{"cmd":100}`, `{"cmd":400}`, `{"cmd":0}`, `{`} {
		if _, err := ParseEvent([]byte(payload)); err == nil {
			t.Fatal("accepted an incomplete event")
		}
	}
}
