# Protocol and dashboard reference

Use this reference only for the local `appleservices-main` implementation or an equivalent Apple Web client.

## Apple repair sequence

The setup base follows the account-country response, not the HME service hostname:

- `CN` or `CHN`: `https://setup.icloud.com.cn/setup/ws/1`
- otherwise: `https://setup.icloud.com/setup/ws/1`

Retain the same account-bound cookie jar throughout the repair.

1. `POST {setupBase}/accountLogin`
   - JSON: `dsWebAuthToken`, `accountCountryCode`, `extended_login: true`, `trustToken`
   - inspect `termsUpdateNeeded`, `dsInfo.languageCode`, `dsInfo.dsid`, and `webservices.premiummailsettings.url`
2. After explicit user authorization, `GET {setupBase}/getTerms`
   - JSON body: `{"locale":"<dsInfo.languageCode or safe locale fallback>"}`
   - require positive integer `iCloudTerms.version`
3. `GET {setupBase}/repairDone`
   - JSON body: `{"acceptedICloudTerms":<exact advertised version>}`
4. Re-run `accountLogin` when the response did not itself issue full Web cookies.

Use `Origin: https://www.icloud.com`, `Referer: https://www.icloud.com/`, the normal Web user agent, JSON content type, no proxy, bounded response sizes, and strict 2xx checks. The GET requests intentionally carry JSON bodies because that is the Apple Web flow.

Never hard-code a terms version. Never submit `repairDone` merely because `X-APPLE-WEBAUTH-REPAIR` exists; explicit authorization is still required.

## Session-state interpretation

Inspect names only, never values:

| Evidence | Meaning |
|---|---|
| `X-APPLE-WEBAUTH-REPAIR` without full tokens | account has a repair/terms blocker |
| `X-APPLE-WEBAUTH-TOKEN` and `X-APPLE-DS-WEB-SESSION-TOKEN` | full iCloud Web session issued |
| repair cookie disappears and full tokens appear | repair transport succeeded; still run HME list |
| valid HME response with zero aliases | feature is available and the account currently has no aliases |

## Local dashboard flow

The dashboard is loopback-only, normally `http://127.0.0.1:18741`.

1. Read the HTML `app-token`; send it as `X-App-Token` without logging it.
2. Confirm `/api/view?account=<account-id>` reports the intended account, live authenticated worker, `control_ready=true`, current family membership, and storage sharing.
3. With explicit terms authorization, call:

   `POST /api/action?account=<account-id>` with `{"action":"hme-accept-terms"}`

4. Independently verify:

   `POST /api/action?account=<account-id>` with `{"action":"hme-list"}`

Completion is `ok=true` and `stage=hme-list`. An alias count of zero is a successful empty mailbox pool.

If the dashboard maps HTTP 400 to “worker does not support HME,” check that the current worker allows `hme-accept-terms`, advertises the `hide-my-email` capability, and was rebuilt/reconnected. Do not label a valid old worker response as “busy”; reserve busy for 503/504 control timeouts.

## Implementation invariants

- `hme-session.json` belongs only to its account directory and stays ignored.
- Save cookies atomically with restrictive permissions.
- Persist safe alias fields only; do not expose the Apple anonymous identifier.
- Preserve unrelated dirty worktree files and do not commit or push unless the user asks.
