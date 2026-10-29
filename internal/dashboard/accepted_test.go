package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appleservices "github.com/Laky-64/appleservices"
)

func TestAcceptedCheckpointRequiresSameBoundAccount(t *testing.T) {
	dir := t.TempDir()
	saved := appleservices.Session{AppleID: "owner@example.test", DSID: "gsa-dsid", Tokens: map[string]string{"com.apple.gs.icloud.storage.buy": "storage-token"}}
	accepted := acceptedICloudCheckpoint{
		Session:     appleservices.Session{AppleID: saved.AppleID, DSID: "icloud-dsid", Tokens: map[string]string{"mmeAuthToken": "mme-token"}},
		ADSID:       saved.DSID,
		ServiceData: map[string]any{"tokens": map[string]any{"mmeAuthToken": "mme-token"}},
	}
	for name, value := range map[string]any{"session.json": saved, "icloud-accepted.json": accepted} {
		data, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !acceptedCheckpointReady(dir, saved.AppleID) {
		t.Fatal("valid bound accepted checkpoint was not detected")
	}
	if acceptedCheckpointReady(dir, "other@example.test") {
		t.Fatal("accepted checkpoint crossed account binding")
	}
	accepted.ADSID = "other-dsid"
	data, _ := json.Marshal(accepted)
	if err := os.WriteFile(filepath.Join(dir, "icloud-accepted.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if acceptedCheckpointReady(dir, saved.AppleID) {
		t.Fatal("accepted checkpoint with mismatched GSA binding was accepted")
	}
}
