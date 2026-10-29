---
name: icloud-hide-my-email-repair
description: Repair and verify an Apple account's iCloud Web session for Hide My Email when accountLogin reports termsUpdateNeeded, X-APPLE-WEBAUTH-REPAIR, missing Web tokens, HTTP 401, or a dashboard repair/upgrade error. Use for read-only HME recovery and validation; not for purchasing iCloud+, changing Family Sharing, or creating aliases without a separate request.
---

# iCloud Hide My Email Repair

Repair the selected account's own Web session, then prove business success with an Apple HME list response.

## Boundaries

- Bind every request to one selected account directory and its independent Mac/ADI identity. Never reuse another account's cookies, password, code, DSID, or device identity.
- Clear proxy environment variables and use a transport with `Proxy=nil` for Apple requests.
- Never print or return passwords, OTPs, cookie values, tokens, DSIDs, PETs, anonymous IDs, or raw authenticated bodies. Report cookie names and boolean state only.
- Accepting updated terms is an external legal mutation. Obtain fresh, explicit authorization for the exact Apple account and currently advertised iCloud terms before calling `repairDone`. Authorization for one account or version does not cover another.
- A read-only HME list needs no extra mutation approval. Creating, reserving, editing, disabling, deleting, or restoring aliases is outside this repair skill unless the user separately requests it.

## Workflow

1. Select the account explicitly and inspect the live worker, family cache, storage-sharing state, and HME capability. Do not diagnose an account from a stale dashboard or another account's worker.
2. Attempt `hme-list`. Treat these as distinct states:
   - `ok=true`, `stage=hme-list`: complete, including a valid empty list.
   - `X-APPLE-WEBAUTH-REPAIR` or `termsUpdateNeeded=true`: terms repair is required.
   - missing `X-APPLE-WEBAUTH-TOKEN` or `X-APPLE-DS-WEB-SESSION-TOKEN`: Web session is incomplete.
   - HTTP 401 from the HME service: authentication failed; it is not evidence that Family Sharing or iCloud+ is absent.
3. Prefer the saved, account-bound repair session. After explicit authorization, fetch the exact advertised version with `getTerms`, submit only that version to `repairDone`, and avoid requesting a fresh SMS code.
4. Re-run `accountLogin` or fresh Web authentication only when necessary. A local generated code is valid only for the challenge that requested it; do not paste arbitrary Mac-generated codes into an unrelated browser challenge. Stop after two explicit invalid-code responses and change paths instead of risking rate limits.
5. Require both session and business verification:
   - repair cookie cleared;
   - full Web token cookie names present;
   - `hme-list` returns `ok=true`, `stage=hme-list`, and an Apple result;
   - WebUI no longer reports repair, unsupported-worker, or busy errors.
6. Preserve the repaired session in the selected ignored account state directory. Do not commit account state or authenticated artifacts.

Read [references/protocol.md](references/protocol.md) when implementing, diagnosing, or manually invoking the repair path in the appleservices checkout.

## Repository finish

When this workflow changes project code or repository-managed skills, finish with a scoped commit and push to the configured private remote. Stage only the files owned by the current change, leave unrelated dirty files untouched, run the relevant tests plus `git diff --check`, and verify the remote branch resolves to the new commit. Never commit account state, logs, binaries, passwords, OTPs, cookies, tokens, DSIDs, PETs, or other authenticated artifacts.

## Stop conditions

- No explicit terms authorization: stop before `repairDone` and state the account and displayed revision/version.
- No saved repair session plus rejected fresh credentials: keep existing state, report the exact sanitized stage, and request only the missing user action.
- Apple returns a rate-limit, security waiting period, or repeated invalid code: stop retries and preserve the session.
- Do not claim completion from HTTP 2xx, cookie transport, or terms submission alone. Only the post-repair HME list proves this workflow is complete.
