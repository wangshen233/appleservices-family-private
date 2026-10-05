package srp

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"testing"
)

// Generated using cocagne/pysrp with rfc5054_enable() and no_username_in_x(),
// and verified against its independent server implementation. Test-only data.
func TestAppleSRPReferenceVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/apple_srp.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Protocol    string `json:"protocol"`
		Username    string `json:"username"`
		Salt        string `json:"salt"`
		ASecret     string `json:"a"`
		A           string `json:"A"`
		B           string `json:"B"`
		X           string `json:"x"`
		PasswordKey string `json:"password_key"`
		SessionKey  string `json:"session_key"`
		M1          string `json:"m1"`
		M2          string `json:"m2"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Protocol, func(t *testing.T) {
			p := NewParams()
			a, ok := new(big.Int).SetString(v.ASecret, 16)
			if !ok {
				t.Fatal("bad fixture ephemeral")
			}
			b, ok := new(big.Int).SetString(v.B, 16)
			if !ok {
				t.Fatal("bad fixture public B")
			}
			public := new(big.Int).Exp(p.G, a, p.N)
			if public.Text(16) != v.A {
				t.Fatal("public A differs from reference")
			}
			salt, err := hex.DecodeString(v.Salt)
			if err != nil {
				t.Fatal(err)
			}
			passwordKey, err := hex.DecodeString(v.PasswordKey)
			if err != nil {
				t.Fatal(err)
			}
			x := p.DeriveX(salt, passwordKey)
			if x.Text(16) != v.X {
				t.Fatal("Apple x derivation differs from reference")
			}
			c := &Client{params: p, username: v.Username, a: a, A: public}
			m1, key, err := c.ProcessChallenge(x, salt, b)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(key) != v.SessionKey || hex.EncodeToString(m1) != v.M1 {
				t.Fatal("session key or client proof differs from independent reference")
			}
			m2, err := hex.DecodeString(v.M2)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.VerifyServer(m2); err != nil {
				t.Fatal(err)
			}
			m2[0] ^= 1
			if err := c.VerifyServer(m2); err == nil {
				t.Fatal("accepted incorrect server proof")
			}
		})
	}
}
