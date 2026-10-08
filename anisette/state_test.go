package anisette

import "testing"

func TestNativePlaceholderIsNotRemoteProvisioning(t *testing.T) {
	for _, blob := range [][]byte{nil, {}, []byte("native-adi")} {
		if (State{AdiPB: blob}).Provisioned() {
			t.Fatal("placeholder skips remote provisioning")
		}
	}
	if !(State{AdiPB: []byte("test-issued-blob")}).Provisioned() {
		t.Fatal("issued remote blob was discarded")
	}
}
