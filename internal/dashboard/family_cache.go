package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
)

const (
	familyCacheFile     = "family-status.json"
	familyCacheVersion  = 1
	familyCacheFreshFor = 15 * time.Minute
)

type familyCacheRecord struct {
	Version   int                `json:"version"`
	AccountID string             `json:"account_id"`
	CheckedAt time.Time          `json:"checked_at"`
	Family    control.FamilyInfo `json:"family"`
}

func validCachedFamily(value *control.FamilyInfo) bool {
	if value == nil || value.Members < 0 || value.Members > 20 || value.Organizers < 0 || value.Organizers > 1 || value.Self < 0 || value.Self > 1 || value.Children < 0 || value.Children > 20 || value.Invitations < 0 || value.Invitations > 20 || value.Pending < 0 || value.Pending > 20 || value.StorageBytes < 0 || value.StorageBytes > 100<<40 {
		return false
	}
	if value.OrganizerAccount != "" && !idPattern.MatchString(value.OrganizerAccount) {
		return false
	}
	if len(value.OrganizerName) > 128 || strings.ContainsAny(value.OrganizerName, "\r\n\x00") {
		return false
	}
	return true
}

func familyCachePath(account *Account) string {
	if account == nil {
		return ""
	}
	return filepath.Join(account.Dir, familyCacheFile)
}

func readFamilyCache(account *Account) (*control.FamilyInfo, time.Time, bool) {
	path := familyCachePath(account)
	if path == "" {
		return nil, time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 64<<10 {
		return nil, time.Time{}, false
	}
	var record familyCacheRecord
	if json.Unmarshal(data, &record) != nil || record.Version != familyCacheVersion || record.AccountID != account.ID || record.CheckedAt.IsZero() || record.CheckedAt.After(time.Now().Add(5*time.Minute)) || !validCachedFamily(&record.Family) {
		return nil, time.Time{}, false
	}
	return &record.Family, record.CheckedAt, time.Since(record.CheckedAt) > familyCacheFreshFor
}

func writeFamilyCache(account *Account, family *control.FamilyInfo) (time.Time, error) {
	if account == nil || !validCachedFamily(family) {
		return time.Time{}, errors.New("invalid family cache binding")
	}
	checkedAt := time.Now().UTC()
	record := familyCacheRecord{Version: familyCacheVersion, AccountID: account.ID, CheckedAt: checkedAt, Family: *family}
	if err := writeSafeCache(familyCachePath(account), record); err != nil {
		return time.Time{}, err
	}
	return checkedAt, nil
}
