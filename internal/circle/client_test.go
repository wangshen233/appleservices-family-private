package circle

import (
	"encoding/asn1"
	"encoding/base64"
	"testing"
)

func TestBothProtocolRolesWithoutOfficialClient(t *testing.T) {
	client, initial, err := NewClient("account", "transaction", "123456789")
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Message{AccountID: "account", Transaction: "transaction", IDMSData: "idms", Step: 1, PAKE: initial.PAKE}, "123456789")
	if err != nil {
		t.Fatal(err)
	}
	challenge := server.InitialResponse()
	if err = server.Commit(1); err != nil {
		t.Fatal(err)
	}
	message := Message{AccountID: "account", Transaction: "transaction", IDMSData: "idms", Step: 2, PAKE: challenge.PAKE}
	proof, err := client.Prove(message, server.Code())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.ProofSent(); err != nil {
		t.Fatal(err)
	}
	message.Step = 3
	message.PAKE = proof.PAKE
	response, err := server.Prepare(message, func() (string, error) { return "654321", nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Commit(3); err != nil {
		t.Fatal(err)
	}
	message.Step = 4
	message.PAKE = response.PAKE
	data, _ := base64.StdEncoding.DecodeString(response.PAKE)
	var decoded step3
	decode(data, &decoded)
	original := append([]byte(nil), decoded.Proof...)
	decoded.Proof[0] ^= 1
	changed, _ := asn1.Marshal(decoded)
	bad := message
	bad.PAKE = base64.StdEncoding.EncodeToString(changed)
	if _, err = client.Receive(bad); err == nil {
		t.Fatal("server proof corruption accepted")
	}
	decoded.Proof = original
	decoded.Payload[len(decoded.Payload)-1] ^= 1
	changed, _ = asn1.Marshal(decoded)
	bad.PAKE = base64.StdEncoding.EncodeToString(changed)
	if _, err = client.Receive(bad); err == nil {
		t.Fatal("GCM tag corruption accepted")
	}
	code, err := client.Receive(message)
	if err != nil || code != "654321" {
		t.Fatal("protocol exchange failed", err)
	}
	bad = message
	bad.Transaction = "different"
	if _, err = client.Receive(bad); err == nil {
		t.Fatal("cross-transaction code accepted")
	}
}
