---
name: apple-account-region-change
description: Change and verify an Apple Account App Store country or region, including prerequisite checks, official UI handoff, and live Store-currency verification. Use for country/region migration before iCloud+ purchase or Family Sharing; do not use to fabricate billing identities or bypass Apple eligibility checks.
---

# Apple Account Region Change

Use Apple’s currently documented account or Media & Purchases surface for the actual region change. The operation is incomplete until the account’s live Store offer uses the target currency; a profile page selection or stale region field is not sufficient evidence.

## Preconditions

- Identify the exact account directory and target country. Never infer the account from a display name when multiple pool entries exist.
- Check Apple’s current official guidance before acting. Clear remaining Store balance, subscriptions that block migration, pending purchases/refunds and existing Family Sharing membership as required.
- Read the current family state before a change. Do not remove or leave a family without explicit authorization for that concrete family.
- Do not invent a payment card, billing address, phone number, tax identity or physical presence. If Apple requires target-country payment or billing details, stop at that form and let the user enter real data locally.
- Passwords, payment data and verification codes stay in the local Apple UI. Do not place them in chat, commands, logs, files or project state.

## Change and verification

1. Open `account.apple.com` or the Windows Apple Music/TV account settings surface Apple currently documents. Keep it visible for local credential and billing entry.
2. Select the requested country or region and present any material blocker. Terms acceptance can proceed only with the account owner’s authorization; payment or billing data remains a user handoff.
3. After the UI reports success, verify from this repository with the accepted iCloud checkpoint:

```powershell
$session = Get-Content 'state/accounts/<account>/session.json' -Raw | ConvertFrom-Json
$env:APPLESERVICES_APPLEID = $session.AppleID
try {
  .\bin\accountsetupcheck.exe `
    -dir 'state/accounts/<account>' `
    -icloud-resume 'state/accounts/<account>/icloud-accepted.json' `
    -storage-plan
} finally {
  Remove-Item Env:APPLESERVICES_APPLEID -ErrorAction SilentlyContinue
}
```

4. Use the returned plan currency and price as the Store-region signal. Apple’s separate `region` field has remained stale on verified migrated accounts and must not override a live target-currency offer.
5. Re-run `go run ./tools/familyinspect -dir state/accounts/<account>` when the migration is for Family Sharing. Require no incompatible current family and a fresh eligible/member result before any family mutation.

For a US migration in this project, the verified 50GB signal is `currency=USD` and `price_minor=99`. A page that says “United States” without that live Store result is only an intermediate state. Continue Family Sharing through the `icloud-family-sharing` skill and purchases through `icloud-plus-purchase`; region migration itself never authorizes either mutation.
