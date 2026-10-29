package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

func TestStorageAndPlanCacheAreAccountBound(t *testing.T) {
	account := &Account{ID: "01-primary", Dir: t.TempDir()}
	storage := &icloud.StorageInfo{TotalBytes: 50 << 30, UsedBytes: 12 << 20, AvailableBytes: (50 << 30) - (12 << 20), PaidBytes: 0, PlanEndpointAvailable: true}
	plans := &icloud.StoragePlanInfo{Region: "CN", Plans: []icloud.StoragePlan{{ID: "com.apple.icloud50GB_monthly", Bytes: 50 << 30, Price: 99, Currency: "USD", Period: "month", Available: true}}}
	if _, err := writeStorageCache(account, storage); err != nil {
		t.Fatal(err)
	}
	if _, err := writePlanCache(account, plans); err != nil {
		t.Fatal(err)
	}
	storageRecord, storageStale := readStorageCache(account, storageCacheFile)
	planRecord, planStale := readStorageCache(account, planCacheFile)
	if storageRecord == nil || storageRecord.Storage.TotalBytes != 50<<30 || storageStale || planRecord == nil || planRecord.Plans.Plans[0].Currency != "USD" || planStale {
		t.Fatalf("unexpected safe cache state storage=%#v/%t plans=%#v/%t", storageRecord, storageStale, planRecord, planStale)
	}
	var raw map[string]any
	data, _ := os.ReadFile(filepath.Join(account.Dir, planCacheFile))
	if json.Unmarshal(data, &raw) != nil || raw["account_id"] != account.ID {
		t.Fatal("plan cache binding is missing")
	}
	other := &Account{ID: "other", Dir: account.Dir}
	if got, _ := readStorageCache(other, storageCacheFile); got != nil {
		t.Fatal("storage cache bound to another account was accepted")
	}
}

func TestStorageCacheRetainsOldResultAsStale(t *testing.T) {
	account := &Account{ID: "01-primary", Dir: t.TempDir()}
	record := storageCacheRecord{Version: storageCacheVersion, AccountID: account.ID, CheckedAt: time.Now().UTC().Add(-storageCacheFreshFor - time.Minute), Storage: &icloud.StorageInfo{TotalBytes: 5 << 30, AvailableBytes: 5 << 30}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(account.Dir, storageCacheFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	got, stale := readStorageCache(account, storageCacheFile)
	if got == nil || !stale {
		t.Fatalf("old storage cache was not retained and marked stale: %#v stale=%t", got, stale)
	}
}

func TestCacheSafeActionResultPersistsPartialReadOnlyResults(t *testing.T) {
	account := &Account{ID: "01-primary", Dir: t.TempDir()}
	result := &control.Result{OK: false, Storage: &icloud.StorageInfo{TotalBytes: 5 << 30, AvailableBytes: 5 << 30}, StoragePlan: &icloud.StoragePlanInfo{Plans: []icloud.StoragePlan{{ID: "plan", Bytes: 50 << 30, Price: 99, Currency: "USD", Period: "month", Available: true}}}}
	cacheSafeActionResult(account, result)
	storage, _ := readStorageCache(account, storageCacheFile)
	plans, _ := readStorageCache(account, planCacheFile)
	if storage == nil || plans == nil {
		t.Fatal("safe partial results were not cached")
	}
}
