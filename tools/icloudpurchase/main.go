// icloudpurchase submits only the explicitly confirmed CN 50GB/CNY 6 monthly
// offer using an already accepted iCloud session, then verifies paid quota.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/nativeadi"
	"howett.net/plist"
)

func run() error {
	dir := flag.String("dir", "", "bound account directory")
	acceptedFile := flag.String("accepted", "", "private accepted iCloud checkpoint")
	journalFile := flag.String("journal", "", "new owner-only attempt journal; never replay an existing attempt")
	confirm := flag.Bool("confirm-cn-50gb", false, "owner has explicitly authorized CN 50GB at CNY 6 per month")
	storeCheck := flag.Bool("store-check-only", false, "check this Mac's native Store authentication without any purchase")
	nativeStore := flag.Bool("native-store", false, "use this account Mac's authenticated native Store and exact returned confirmation")
	localStoreCode := flag.Bool("local-store-code", false, "generate a code on this account's existing trusted Mac; never request SMS")
	storeRetries := flag.Int("store-auth-retries", 0, "at most 3 routing-error retries during Store authentication; never retry a purchase")
	signedStore := flag.Bool("signed-store", false, "SAP-sign this Mac's native Store authentication")
	appStoreProfile := flag.Bool("appstore-profile", false, "use the exact Apple bag endpoint and account Mac App Store headers with SAP")
	claudeSkill := flag.Bool("claude-skill", false, "follow .claude icloud-plus-purchase with physical Store GUID and native plist flow")
	storeSessionOut := flag.String("store-session-out", "", "new owner-only output retaining this account's Store credentials and cookies")
	storeSessionIn := flag.String("store-session-in", "", "private Store session for this account and physical GUID")
	flag.Parse()
	if *claudeSkill && !*nativeStore {
		return fmt.Errorf("claude-skill requires native-store")
	}
	if *storeSessionIn != "" && (!*claudeSkill || !*nativeStore) {
		return fmt.Errorf("private Store resume requires the native purchase skill")
	}
	if *storeRetries < 0 || *storeRetries > 3 {
		return fmt.Errorf("Store authentication retries must be between 0 and 3")
	}
	if !*confirm || *dir == "" || *acceptedFile == "" || *journalFile == "" {
		return fmt.Errorf("explicit purchase confirmation, account, accepted checkpoint and new private journal required")
	}
	if _, err := os.Stat(*journalFile); !os.IsNotExist(err) {
		return fmt.Errorf("attempt journal already exists; inspect the result and quota before any retry")
	}
	username, password := os.Getenv("APPLESERVICES_APPLEID"), os.Getenv("APPLESERVICES_PASSWORD")
	storeCode := os.Getenv("APPLESERVICES_STORE_CODE")
	os.Unsetenv("APPLESERVICES_APPLEID")
	os.Unsetenv("APPLESERVICES_PASSWORD")
	os.Unsetenv("APPLESERVICES_STORE_CODE")
	if *localStoreCode && (!*nativeStore && !*storeCheck || storeCode != "") {
		return fmt.Errorf("local-store-code requires native Store mode and cannot combine with a supplied code")
	}
	if username == "" || (password == "" && *storeSessionIn == "") {
		return fmt.Errorf("credentials required through environment")
	}
	var saved appleservices.Session
	data, err := os.ReadFile(filepath.Join(*dir, "session.json"))
	if err != nil || json.Unmarshal(data, &saved) != nil || !strings.EqualFold(saved.AppleID, username) || saved.DSID == "" {
		return fmt.Errorf("account session binding failed")
	}
	var accepted struct {
		Session     appleservices.Session `json:"session"`
		ADSID       string                `json:"adsid"`
		ServiceData map[string]any        `json:"service-data"`
	}
	data, err = os.ReadFile(*acceptedFile)
	if err != nil || json.Unmarshal(data, &accepted) != nil || !strings.EqualFold(accepted.Session.AppleID, username) || accepted.ADSID != saved.DSID || accepted.Session.DSID == "" || accepted.Session.Tokens["mmeAuthToken"] == "" {
		return fmt.Errorf("accepted iCloud checkpoint account binding failed")
	}
	var device appleservices.Device
	data, err = os.ReadFile(filepath.Join(*dir, "device.json"))
	if err != nil || json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 || device.SerialNumber == "" {
		return fmt.Errorf("bound device unavailable")
	}
	profile := filepath.Join(*dir, "native-adi")
	var hardware struct {
		Push struct {
			Token []byte `plist:"token"`
		} `plist:"push"`
	}
	data, err = os.ReadFile(filepath.Join(profile, "hw_info.plist"))
	if err != nil {
		return fmt.Errorf("hardware unavailable")
	}
	if _, err = plist.Unmarshal(data, &hardware); err != nil || len(hardware.Push.Token) != 32 {
		return fmt.Errorf("account Mac APS token unavailable")
	}
	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	native, err := nativeadi.NewClient(nativeadi.Config{LibraryPath: library, ProfileDir: profile, Python: "python", Identifier: device.Identifier, Serial: device.SerialNumber, PushToken: hardware.Push.Token})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := native.Start(ctx); err != nil {
		return err
	}
	defer native.Close()
	headers, err := native.Headers()
	if err != nil {
		return err
	}
	token := accepted.Session.Tokens["mmeAuthToken"]
	quota, err := icloud.FetchStorageInfo(ctx, accepted.ServiceData, username, accepted.Session.DSID, accepted.ADSID, token, headers)
	if err != nil {
		return err
	}
	fmt.Printf("pre-purchase quota; total-bytes=%d; paid-bytes=%d\n", quota.TotalBytes, quota.PaidBytes)
	// Do not submit any upgrade while a paid plan is already present.
	if quota.PaidBytes != 0 {
		return fmt.Errorf("existing paid capacity detected; purchase not submitted")
	}
	target := icloud.PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}
	storageToken := saved.Tokens["com.apple.gs.icloud.storage.buy"]
	plans, err := icloud.FetchStorefrontPlanInfoWithStorageToken(ctx, username, accepted.Session.DSID, accepted.ADSID, token, storageToken, headers)
	if err != nil {
		return err
	}
	if err := icloud.ValidatePurchaseTarget(plans, target); err != nil {
		return err
	}
	fmt.Println("exact Apple offer verified: CN 50GB CNY 600 minor units/month")
	journal, err := os.OpenFile(*journalFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot create new private attempt journal")
	}
	defer journal.Close()
	enc := json.NewEncoder(journal)
	binding := sha256.Sum256([]byte(username + "\x00" + saved.DSID + "\x00" + fmt.Sprintf("%x", device.Identifier)))
	if err := enc.Encode(map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "binding-sha256": fmt.Sprintf("%x", binding), "stage": "started", "target": target}); err != nil {
		return fmt.Errorf("cannot record purchase intent")
	}
	if err := journal.Sync(); err != nil {
		return fmt.Errorf("cannot preserve purchase intent")
	}
	var commerce *icloud.StoreCommerceSession
	if *storeCheck || *nativeStore {
		if *localStoreCode {
			storeCode, err = native.Code(ctx)
			if err != nil {
				return fmt.Errorf("this account Mac's local verification code is unavailable")
			}
			fmt.Println("same-account local Mac code generated in memory; no SMS requested")
		}
		if *storeSessionIn != "" {
			commerce, err = icloud.LoadPrivateSkillSession(*storeSessionIn, username, accepted.Session.DSID)
		} else if *claudeSkill {
			fmt.Println("repository purchase skill: physical host Store GUID; account Mac retained for iCloud")
			var provider func() (string, error)
			if *localStoreCode {
				provider = func() (string, error) { return native.Code(ctx) }
			} else if storeCode != "" {
				provider = func() (string, error) { return storeCode, nil }
			}
			commerce, err = icloud.AuthenticateSkillStore(ctx, username, password, provider)
		} else if *appStoreProfile {
			fmt.Println("native App Store profile; signed exact Apple bag endpoint; account Mac retained")
			commerce, err = icloud.AuthenticateAppStoreForHardware(ctx, username, password, storeCode, headers, nativeadi.SyntheticMACAddress(device.Identifier), *storeRetries)
		} else if *signedStore {
			fmt.Println("native Store SAP signature enabled; account Mac hardware retained")
			commerce, err = icloud.AuthenticateSignedStoreForHardware(ctx, username, password, storeCode, headers, nativeadi.SyntheticMACAddress(device.Identifier), *storeRetries)
		} else {
			commerce, err = icloud.AuthenticateStoreForHardwareWithRetries(ctx, username, password, storeCode, headers, nativeadi.SyntheticMACAddress(device.Identifier), *storeRetries)
		}
		storeCode = ""
		status := map[string]any{"stage": "store-authentication", "purchase-submitted": false, "store-ready": err == nil && commerce.MatchesAccount(accepted.Session.DSID)}
		if err != nil {
			status["error"] = err.Error()
		}
		if err := enc.Encode(status); err != nil {
			return fmt.Errorf("cannot preserve Store check result")
		}
		journal.Sync()
		encoded, _ := json.Marshal(status)
		fmt.Println(string(encoded))
		if err != nil {
			return err
		}
		if !commerce.MatchesAccount(accepted.Session.DSID) {
			return fmt.Errorf("Store account binding failed; purchase not submitted")
		}
		if *storeSessionOut != "" {
			if err := commerce.SavePrivateSkillSession(*storeSessionOut, username); err != nil {
				return err
			}
		}
		if *storeCheck {
			return nil
		}
	}
	// One call only. Never replay an ambiguous purchase response automatically.
	var result *icloud.PurchaseResult
	var purchaseErr error
	if *claudeSkill {
		before, err := commerce.ReadSkillSubscription(ctx)
		if err != nil {
			return err
		}
		if before.Present {
			encoded, _ := json.Marshal(before)
			fmt.Printf("existing iCloud subscription; purchase not submitted; %s\n", encoded)
			return nil
		}
		result, purchaseErr = commerce.SubmitSkillPurchase(ctx, plans, target, "290399")
	} else if *nativeStore {
		result, purchaseErr = commerce.SubmitConfirmedStoragePurchase(ctx, plans, username, password, accepted.Session.DSID, accepted.ADSID, token, storageToken, headers, target)
	} else {
		result, purchaseErr = icloud.SubmitStoragePurchaseAnonymous(ctx, plans, username, password, accepted.Session.DSID, accepted.ADSID, token, storageToken, headers, target)
	}
	password = ""
	// Verify quota even when the returned marker or transport is ambiguous.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer verifyCancel()
	updated, quotaErr := icloud.FetchStorageInfo(verifyCtx, accepted.ServiceData, username, accepted.Session.DSID, accepted.ADSID, token, headers)
	status := map[string]any{"stage": "returned", "purchase-result": result, "quota-read-success": quotaErr == nil}
	var subscription *icloud.SkillSubscriptionInfo
	var subscriptionErr error
	if *claudeSkill {
		subscription, subscriptionErr = commerce.ReadSkillSubscription(verifyCtx)
		status["subscription"] = subscription
		if subscriptionErr != nil {
			status["subscription-error"] = subscriptionErr.Error()
		}
	}
	if purchaseErr != nil {
		status["purchase-error"] = purchaseErr.Error()
		if e, ok := purchaseErr.(*icloud.StorageError); ok && e.AppleCode != "" {
			status["apple-code"] = e.AppleCode
		}
	}
	if quotaErr != nil {
		status["quota-error"] = quotaErr.Error()
	}
	if updated != nil {
		status["total-bytes"], status["paid-bytes"] = updated.TotalBytes, updated.PaidBytes
	}
	completed := result != nil && result.State == "completed" && purchaseErr == nil && quotaErr == nil && updated.TotalBytes >= target.Bytes && updated.PaidBytes >= target.Bytes
	if *claudeSkill {
		completed = result != nil && result.State == "completed" && purchaseErr == nil && subscriptionErr == nil && subscription != nil && subscription.Verified50GB
	}
	status["purchase-completed-and-quota-verified"] = completed
	if err := enc.Encode(status); err != nil {
		return fmt.Errorf("cannot preserve attempt result; inspect quota before retry")
	}
	if err := journal.Sync(); err != nil {
		return fmt.Errorf("cannot preserve attempt result; inspect quota before retry")
	}
	encoded, _ := json.Marshal(status)
	fmt.Println(string(encoded))
	if !completed {
		return fmt.Errorf("subscription completion not verified; do not repeat this attempt blindly")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
