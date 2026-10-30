package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
)

func TestRequireHMEFamilyMemberRejectsOrganizerAndStaleCache(t *testing.T) {
	now := time.Now().UTC()
	write := func(dir string, checkedAt time.Time, family control.FamilyInfo) {
		record := hmeFamilyCache{Version: 1, AccountID: filepath.Base(dir), CheckedAt: checkedAt, Family: family}
		data, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(dir, "family-status.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	member := control.FamilyInfo{Member: true, Organizers: 1, Self: 1, StorageKnown: true, StorageParticipating: true, StorageAvailable: true, StorageEnabled: true, StorageBytes: 50 << 30}
	dir := filepath.Join(t.TempDir(), "01-primary")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write(dir, now, member)
	if err := requireHMEFamilyMember(dir, now); err != nil {
		t.Fatal(err)
	}
	member.Organizer = true
	write(dir, now, member)
	if err := requireHMEFamilyMember(dir, now); err == nil {
		t.Fatal("organizer was accepted")
	}
	member.Organizer = false
	write(dir, now.Add(-16*time.Minute), member)
	if err := requireHMEFamilyMember(dir, now); err == nil {
		t.Fatal("stale family cache was accepted")
	}
}
