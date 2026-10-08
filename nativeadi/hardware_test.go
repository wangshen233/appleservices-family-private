package nativeadi

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSyntheticMacIdentityIsStableAndSeparated(t *testing.T) {
	seen := map[string]bool{}
	for i := uint64(0); i < 4096; i++ {
		identifier := make([]byte, 16)
		binary.BigEndian.PutUint64(identifier[8:], i)
		original := append([]byte(nil), identifier...)
		first := SyntheticMACAddress(identifier)
		second := SyntheticMACAddress(identifier)
		if !bytes.Equal(first, second) || !bytes.Equal(identifier, original) {
			t.Fatal("derivation changed the device or was not stable")
		}
		if len(first) != 6 || first[0]&3 != 2 || seen[string(first)] {
			t.Fatal("diagnostic devices shared an address or lacked a local unicast address")
		}
		seen[string(first)] = true
		first[0] ^= 0xff
		if !bytes.Equal(SyntheticMACAddress(identifier), second) {
			t.Fatal("caller mutation changed a future device address")
		}
	}
}
