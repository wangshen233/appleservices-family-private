package gsa

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPasswordDerivationReferenceVectors(t *testing.T) {
	data, err := os.ReadFile("../internal/srp/testdata/apple_srp.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Protocol    string `json:"protocol"`
		Password    string `json:"password"`
		Salt        string `json:"salt"`
		Iterations  int    `json:"iterations"`
		PasswordKey string `json:"password_key"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Protocol, func(t *testing.T) {
			salt, err := hex.DecodeString(v.Salt)
			if err != nil {
				t.Fatal(err)
			}
			key, err := derivePasswordKey(v.Password, v.Protocol, salt, v.Iterations)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(key) != v.PasswordKey {
				t.Fatal("password KDF differs from independent reference")
			}
		})
	}
}
