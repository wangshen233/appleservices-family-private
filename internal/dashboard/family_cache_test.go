package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
)

func TestFamilyCacheRoundTripAndReplacement(t *testing.T) {
	account := &Account{ID: "04-account", Name: "organizer", Dir: t.TempDir()}
	family := &control.FamilyInfo{Member: true, Members: 3, Organizers: 1, Self: 1, Organizer: true, OrganizerAccount: account.ID, OrganizerName: account.Name, StorageKnown: true, StorageParticipating: true, StorageAvailable: true, StorageEnabled: true, StorageBytes: 50 << 30}
	first, err := writeFamilyCache(account, family)
	if err != nil {
		t.Fatal(err)
	}
	family.Members = 4
	second, err := writeFamilyCache(account, family)
	if err != nil {
		t.Fatal(err)
	}
	got, checkedAt, stale := readFamilyCache(account)
	if got == nil || got.Members != 4 || !got.Organizer || got.OrganizerAccount != account.ID || stale || checkedAt.Before(first) || second.Before(first) {
		t.Fatalf("unexpected cache state: %#v checked=%v stale=%t", got, checkedAt, stale)
	}
	info, err := os.Stat(familyCachePath(account))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("family cache permissions are not owner-only: %v %#o", err, info.Mode().Perm())
	}
}

func TestFamilyCacheRejectsWrongAccountAndMarksOldStateStale(t *testing.T) {
	account := &Account{ID: "01-primary", Dir: t.TempDir()}
	record := familyCacheRecord{Version: familyCacheVersion, AccountID: "other", CheckedAt: time.Now().UTC(), Family: control.FamilyInfo{Member: true, Members: 3, Organizers: 1, Self: 1}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(account.Dir, familyCacheFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := readFamilyCache(account); got != nil {
		t.Fatal("cache bound to another account was accepted")
	}
	record.AccountID = account.ID
	record.CheckedAt = time.Now().UTC().Add(-familyCacheFreshFor - time.Minute)
	data, _ = json.Marshal(record)
	if err := os.WriteFile(filepath.Join(account.Dir, familyCacheFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	got, _, stale := readFamilyCache(account)
	if got == nil || !stale {
		t.Fatalf("old family cache was not retained and marked stale: %#v stale=%t", got, stale)
	}
}
