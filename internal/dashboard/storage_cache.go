package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Laky-64/appleservices/icloud"
)

const (
	storageCacheVersion  = 1
	storageCacheFreshFor = 10 * time.Minute
	storageCacheFile     = "storage-status.json"
	planCacheFile        = "storage-plans.json"
)

type storageCacheRecord struct {
	Version   int                     `json:"version"`
	AccountID string                  `json:"account_id"`
	CheckedAt time.Time               `json:"checked_at"`
	Storage   *icloud.StorageInfo     `json:"storage,omitempty"`
	Plans     *icloud.StoragePlanInfo `json:"plans,omitempty"`
}

func validStorageInfo(info *icloud.StorageInfo) bool {
	return info != nil && info.TotalBytes > 0 && info.UsedBytes <= info.TotalBytes && info.AvailableBytes <= info.TotalBytes && info.PaidBytes <= info.TotalBytes
}

func validStoragePlans(info *icloud.StoragePlanInfo) bool {
	if info == nil || len(info.Plans) == 0 || len(info.Plans) > 32 {
		return false
	}
	for _, plan := range info.Plans {
		if plan.Bytes == 0 || plan.Bytes > 100<<40 || len(plan.ID) > 256 || len(plan.Currency) > 8 || len(plan.Period) > 32 {
			return false
		}
	}
	return true
}

func writeSafeCache(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".dashboard-cache-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func writeStorageCache(account *Account, storage *icloud.StorageInfo) (time.Time, error) {
	if account == nil || !validStorageInfo(storage) {
		return time.Time{}, errors.New("invalid storage cache binding")
	}
	checkedAt := time.Now().UTC()
	record := storageCacheRecord{Version: storageCacheVersion, AccountID: account.ID, CheckedAt: checkedAt, Storage: storage}
	return checkedAt, writeSafeCache(filepath.Join(account.Dir, storageCacheFile), record)
}

func writePlanCache(account *Account, plans *icloud.StoragePlanInfo) (time.Time, error) {
	if account == nil || !validStoragePlans(plans) {
		return time.Time{}, errors.New("invalid plan cache binding")
	}
	checkedAt := time.Now().UTC()
	record := storageCacheRecord{Version: storageCacheVersion, AccountID: account.ID, CheckedAt: checkedAt, Plans: plans}
	return checkedAt, writeSafeCache(filepath.Join(account.Dir, planCacheFile), record)
}

func readStorageCache(account *Account, name string) (*storageCacheRecord, bool) {
	if account == nil || (name != storageCacheFile && name != planCacheFile) {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(account.Dir, name))
	if err != nil || len(data) == 0 || len(data) > 128<<10 {
		return nil, false
	}
	var record storageCacheRecord
	if json.Unmarshal(data, &record) != nil || record.Version != storageCacheVersion || record.AccountID != account.ID || record.CheckedAt.IsZero() || record.CheckedAt.After(time.Now().Add(5*time.Minute)) {
		return nil, false
	}
	if name == storageCacheFile && !validStorageInfo(record.Storage) {
		return nil, false
	}
	if name == planCacheFile && !validStoragePlans(record.Plans) {
		return nil, false
	}
	return &record, time.Since(record.CheckedAt) > storageCacheFreshFor
}
