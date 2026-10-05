package httpx

import (
	"maps"
	"regexp"
	"strings"
)

var legacyXcodeClient = regexp.MustCompile(`com\.apple\.dt\.Xcode(?:/[^\s()<>]+)?`)

// NormalizeClientInfo applies the September 2026 upstream compatibility fix.
// Keep the provider's hardware and OS identity; change only the retired bundle token.
func NormalizeClientInfo(info string) string {
	return legacyXcodeClient.ReplaceAllString(info, "com.apple.akd/1.0")
}

// NormalizeAnisetteHeaders also covers externally supplied anisette providers.
// Return a copy so the provider's map is not changed by authentication code.
func NormalizeAnisetteHeaders(headers map[string]string) map[string]string {
	out := maps.Clone(headers)
	for k, v := range headers {
		if strings.EqualFold(k, "X-Mme-Client-Info") {
			delete(out, k)
			out["X-Mme-Client-Info"] = NormalizeClientInfo(v)
		}
	}
	return out
}
