package icloud

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// StoreFormInfo is a value-free description of the form returned by Apple.
// It excludes query parameters, input values and response text.
type StoreFormInfo struct {
	HTTPStatus         int                `json:"http_status"`
	BodyBytes          int                `json:"body_bytes"`
	ContentType        string             `json:"content_type"`
	ActionPath         string             `json:"action_path,omitempty"`
	ActionHost         string             `json:"action_host,omitempty"`
	BaseHost           string             `json:"base_host,omitempty"`
	BasePath           string             `json:"base_path,omitempty"`
	Method             string             `json:"method,omitempty"`
	Controls           []StoreControlInfo `json:"controls"`
	CookieNames        []string           `json:"cookie_names"`
	Scripts            []string           `json:"scripts"`
	Bindings           map[string]bool    `json:"bindings"`
	ContinuationPath   string             `json:"continuation_path,omitempty"`
	ContinuationFields []string           `json:"continuation_fields,omitempty"`
}

type StoreControlInfo struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	ValueBytes       int    `json:"value_bytes"`
	Disabled         bool   `json:"disabled"`
	HasExplicitOwner bool   `json:"has_explicit_owner"`
}

var storeControlName = regexp.MustCompile(`^[A-Za-z0-9_.:-]{0,80}$`)
var storeScriptSrc = regexp.MustCompile(`(?i)<script[^>]*\bsrc=["']([^"']+)["']`)
var cookieIDPattern = regexp.MustCompile(`[0-9]{5,}`)

// PreviewStoragePurchase reads the selected offer's form without any POST.
func (s *StoreCommerceSession) PreviewStoragePurchase(ctx context.Context, info *StoragePlanInfo, appleID, dsid, adsid, token, storageBuyToken string, headers map[string]string, target PurchaseTarget) (*StoreFormInfo, error) {
	if err := ValidatePurchaseTarget(info, target); err != nil {
		return nil, err
	}
	if s == nil || s.client == nil || s.identity == nil {
		return nil, &StorageError{Stage: "purchase-store-session"}
	}
	query := url.Values{"appleId": {appleID}, "dsid": {dsid}, "authToken": {token}}
	get := func(endpoint, referer string) (*http.Response, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, nil, &StorageError{Stage: "purchase-preview-request"}
		}
		setStoreHeaders(req, headers, adsid, referer, dsid, token)
		s.identity.setHeaders(req)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, nil, &StorageError{Stage: "purchase-preview-transport"}
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxStoreBagBytes+1))
		if err != nil || len(body) > maxStoreBagBytes {
			return nil, nil, &StorageError{Stage: "purchase-preview-response", HTTPStatus: resp.StatusCode}
		}
		if resp.StatusCode != 200 {
			return nil, nil, &StorageError{Stage: "purchase-preview", HTTPStatus: resp.StatusCode, Hint: storeResponseHint(body, resp.Header.Get("Content-Type"))}
		}
		return resp, body, nil
	}
	presentURL := info.sourceEndpoint
	if presentURL == "" {
		presentURL = storePresentOffersURL + "?" + query.Encode()
	}
	_, body, err := get(presentURL, "")
	if err != nil {
		return nil, err
	}
	offerURL, err := exactOfferURL(body, target)
	if err != nil {
		return nil, &StorageError{Stage: "purchase-offer"}
	}
	resp, body, err := get(offerURL, presentURL)
	if err != nil {
		return nil, err
	}
	result := &StoreFormInfo{HTTPStatus: resp.StatusCode, BodyBytes: len(body), ContentType: resp.Header.Get("Content-Type"), Controls: []StoreControlInfo{}, CookieNames: []string{}, Scripts: []string{}, Bindings: map[string]bool{}}
	for _, form := range parseStoreForms(body) {
		if !strings.Contains(form.action, "volumeAuthenticate") {
			continue
		}
		u, err := url.Parse(form.action)
		if err != nil {
			return nil, &StorageError{Stage: "purchase-endpoint"}
		}
		result.ActionPath, result.Method = u.EscapedPath(), form.method
		postURL, err := storeFormURL(resp.Request.URL, body, form.action)
		if err != nil {
			return nil, err
		}
		result.ActionHost, result.ActionPath = postURL.Hostname(), postURL.EscapedPath()
		if base := storeDocumentBase(body); base != "" {
			if baseURL, err := url.Parse(base); err == nil {
				result.BaseHost, result.BasePath = baseURL.Hostname(), baseURL.EscapedPath()
			}
		}
		for _, field := range form.fields {
			if !storeControlName.MatchString(field.name) {
				continue
			}
			result.Controls = append(result.Controls, StoreControlInfo{Name: field.name, Type: field.typ, ValueBytes: len(field.value), Disabled: field.disabled, HasExplicitOwner: field.owner != ""})
			switch field.name {
			case "xToken":
				result.Bindings["store_token_matches"] = field.value == s.identity.PasswordToken
			case "prsId":
				result.Bindings["store_account_matches"] = field.value == s.identity.DSID
			case "authToken":
				result.Bindings["icloud_token_matches"] = field.value == token
			case "appleId":
				result.Bindings["apple_id_matches"] = strings.EqualFold(field.value, appleID)
			case "okURL":
				if nextURL, err := url.Parse(field.value); err == nil {
					result.ContinuationPath = nextURL.EscapedPath()
					q := nextURL.Query()
					for key := range q {
						if storeControlName.MatchString(key) {
							result.ContinuationFields = append(result.ContinuationFields, key)
						}
					}
					sort.Strings(result.ContinuationFields)
					result.Bindings["continuation_store_token_matches"] = q.Get("xToken") == s.identity.PasswordToken
					result.Bindings["continuation_guid_matches"] = q.Get("guid") != "" && q.Get("guid") == successfulStoreFormValues(form).Get("guid")
					result.Bindings["continuation_offer_matches"] = q.Get("offerName") == "com.apple.icloud50GB_monthly" && q.Get("storagePlanInBytes") == "53687091200" && q.Get("price") == "6000" && q.Get("cc") == "CN"
				}
			}
		}
		break
	}
	u, _ := url.Parse(offerURL)
	for _, cookie := range s.client.Jar.Cookies(u) {
		if storeControlName.MatchString(cookie.Name) {
			result.CookieNames = append(result.CookieNames, cookieIDPattern.ReplaceAllString(cookie.Name, "[id]"))
		}
	}
	sort.Strings(result.CookieNames)
	for _, match := range storeScriptSrc.FindAllSubmatch(body, -1) {
		u, err := url.Parse(string(match[1]))
		if err != nil {
			continue
		}
		// Paths are allowlisted; account identifiers may appear in arbitrary URLs.
		if strings.Contains(u.Path, "itunes") || strings.HasSuffix(u.Path, ".js") {
			result.Scripts = append(result.Scripts, u.EscapedPath())
		}
	}
	return result, nil
}
