// familyinspect performs read-only discovery of Apple's Family Sharing account
// endpoint and, when available, returns a redacted membership summary.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appleservices "github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/nativeadi"
	"github.com/google/uuid"
	"howett.net/plist"
)

const configurationURL = "https://setup.icloud.com/configurations/init?context=settings"

type acceptedCheckpoint struct {
	Session     appleservices.Session `json:"session"`
	ADSID       string                `json:"adsid"`
	ServiceData map[string]any        `json:"service-data"`
}

func decodeObject(data []byte) (map[string]any, error) {
	var value map[string]any
	if json.Unmarshal(data, &value) == nil {
		return value, nil
	}
	if _, err := plist.Unmarshal(data, &value); err == nil {
		return value, nil
	}
	return nil, errors.New("response is not JSON or plist")
}

func appleEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	host := strings.ToLower(u.Hostname())
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" ||
		!(host == "icloud.com" || strings.HasSuffix(host, ".icloud.com") || host == "apple.com" || strings.HasSuffix(host, ".apple.com")) {
		return nil, errors.New("family endpoint is outside Apple HTTPS")
	}
	return u, nil
}

func readLimited(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("family response is invalid")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("family request returned HTTP %d", resp.StatusCode)
	}
	return data, nil
}

func tokenHeader(dsid, token string) string {
	return base64.StdEncoding.EncodeToString([]byte(dsid + ":" + token))
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func run() error {
	dir := flag.String("dir", "", "bound account directory")
	gsaCheckpoint := flag.String("gsa", "", "optional fresh private GSA checkpoint for family app tokens")
	dump := flag.String("dump", "", "new private raw family response")
	listEndpoints := flag.Bool("list-endpoints", false, "print safe family endpoint keys, hosts and paths")
	flag.Parse()
	if *dir == "" {
		return errors.New("bound account directory is required")
	}
	var saved appleservices.Session
	data, err := os.ReadFile(filepath.Join(*dir, "session.json"))
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.AppleID == "" || saved.DSID == "" {
		return errors.New("bound account session unavailable")
	}
	auth := saved
	if *gsaCheckpoint != "" {
		data, err = os.ReadFile(*gsaCheckpoint)
		if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &auth) != nil || !strings.EqualFold(auth.AppleID, saved.AppleID) || auth.DSID != saved.DSID {
			return errors.New("fresh GSA checkpoint account binding failed")
		}
	}
	var accepted acceptedCheckpoint
	data, err = os.ReadFile(filepath.Join(*dir, "icloud-accepted.json"))
	if err != nil || json.Unmarshal(data, &accepted) != nil || !strings.EqualFold(saved.AppleID, accepted.Session.AppleID) || accepted.ADSID != saved.DSID || accepted.Session.DSID == "" || accepted.Session.Tokens["mmeAuthToken"] == "" {
		return errors.New("accepted iCloud checkpoint unavailable")
	}
	icloudToken, familyToken := auth.Tokens["com.apple.gs.icloud.auth"], auth.Tokens["com.apple.gs.icloud.family.auth"]
	if icloudToken == "" || familyToken == "" {
		return errors.New("family GrandSlam tokens unavailable")
	}
	var device appleservices.Device
	data, err = os.ReadFile(filepath.Join(*dir, "device.json"))
	if err != nil || json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 || device.SerialNumber == "" {
		return errors.New("bound device unavailable")
	}
	var hardware struct {
		Push struct {
			Token []byte `plist:"token"`
		} `plist:"push"`
	}
	profile := filepath.Join(*dir, "native-adi")
	data, err = os.ReadFile(filepath.Join(profile, "hw_info.plist"))
	if err != nil {
		return errors.New("bound Mac hardware unavailable")
	}
	if _, err := plist.Unmarshal(data, &hardware); err != nil || len(hardware.Push.Token) != 32 {
		return errors.New("bound APS token unavailable")
	}
	library, _ := filepath.Abs(".cache/openbubbles-store/rust_lib_bluebubbles.dll")
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	native, err := nativeadi.NewClient(nativeadi.Config{LibraryPath: library, ProfileDir: profile, Python: "python", Identifier: device.Identifier, Serial: device.SerialNumber, PushToken: hardware.Push.Token})
	if err != nil {
		return err
	}
	if err := native.Start(ctx); err != nil {
		return err
	}
	defer native.Close()
	headers, err := native.Headers()
	if err != nil {
		return err
	}
	transport := gsa.AppleRootTransport().(*http.Transport)
	// setup.icloud.com/configurations/init currently stalls on this Windows
	// route after HTTP/2 negotiation while the same request succeeds over 1.1.
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, configurationURL, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("User-Agent", "Setup/1.0 CFNetwork/1408.0.4 Darwin/22.5.0")
	request.Header.Set("X-MMe-Country", "CN")
	request.Header.Set("X-MMe-Language", "en-US")
	request.Header.Set("X-Apple-Locale", "en_US")
	request.Header.Set("X-MMe-Nac-Version", "22C65")
	resp, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("family configuration transport failed: %w", err)
	}
	configurationData, err := readLimited(resp)
	if err != nil {
		return err
	}
	configuration, err := decodeObject(configurationData)
	if err != nil {
		return err
	}
	urls, _ := configuration["urls"].(map[string]any)
	if urls == nil {
		return errors.New("family configuration has no URL bag")
	}
	keys := make([]string, 0)
	for key := range urls {
		if strings.Contains(strings.ToLower(key), "family") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	fmt.Printf("Apple family URL bag loaded; family-keys=%d\n", len(keys))
	if *listEndpoints {
		for _, key := range keys {
			u, err := appleEndpoint(stringValue(urls[key]))
			if err == nil {
				fmt.Printf("family endpoint key=%s host=%s path=%s\n", key, u.Hostname(), u.EscapedPath())
			}
		}
	}
	rawDetails := stringValue(urls["getFamilyDetails"])
	if rawDetails == "" {
		rawDetails = stringValue(urls["getFamilyDetailsURL"])
	}
	if rawDetails == "" {
		return errors.New("Apple bag did not return a family-details endpoint")
	}
	detailsURL, err := appleEndpoint(rawDetails)
	if err != nil {
		return err
	}
	newFamilyRequest := func(endpoint string, fresno bool) (*http.Request, error) {
		u, err := appleEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		var body []byte
		if fresno {
			accountDetails := map[string]any{
				"appleID":       saved.AppleID,
				"dsid":          accepted.Session.DSID,
				"altDSID":       saved.DSID,
				"dsFamilyToken": familyToken,
			}
			body, err = plist.Marshal(map[string]any{"iCloudAccountDetails": accountDetails, "iTunesAccountDetails": accountDetails}, plist.XMLFormat)
			if err != nil {
				return nil, errors.New("cannot encode family account binding")
			}
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		request.Header.Set("User-Agent", "com.apple.Preferences/1.0 CFNetwork/1408.0.4 Darwin/22.5.0")
		request.Header.Set("X-MMe-Client-Info", "<MacBookPro15,1> <Mac OS X;13.1;22C65> <com.apple.AppleAccount/1.0 (com.apple.systempreferences.AppleIDSettings/1)>")
		request.Header.Set("X-MMe-Country", "CN")
		request.Header.Set("X-MMe-Language", "en-US")
		request.Header.Set("Accept", "application/json, text/plist, application/x-plist")
		request.Header.Set("Accept-Language", "en-US")
		if fresno {
			request.Header.Set("Content-Type", "application/x-plist")
			request.Header.Set("X-APNS-Push-Token", strings.ToUpper(fmt.Sprintf("%x", hardware.Push.Token)))
		}
		request.Header.Set("Authorization", "Basic "+tokenHeader(accepted.Session.DSID, accepted.Session.Tokens["mmeAuthToken"]))
		// AAGrandSlamSigner signs both app-token headers with the GrandSlam
		// account DSID/ADSID binding, kept separately from the iCloud DSID.
		request.Header.Set("X-Apple-GS-Token", tokenHeader(saved.DSID, icloudToken))
		request.Header.Set("X-Apple-Family-GS-Token", tokenHeader(saved.DSID, familyToken))
		request.Header.Set("X-MMe-LoggedIn-AppleID", saved.AppleID)
		request.Header.Set("X-MMe-Device-Id", strings.ToUpper(uuid.UUID(device.Identifier).String()))
		request.Header.Set("X-MMe-FMFAllowed", "false")
		if value := headerValue(headers, "X-Apple-I-MD"); value != "" {
			request.Header.Set("X-Apple-MD", value)
		}
		if value := headerValue(headers, "X-Apple-I-MD-M"); value != "" {
			request.Header.Set("X-Apple-MD-M", value)
		}
		return request, nil
	}
	detailsRequest, err := newFamilyRequest(detailsURL.String(), true)
	if err != nil {
		return err
	}
	detailsResponse, err := client.Do(detailsRequest)
	if err != nil {
		return fmt.Errorf("family details transport failed: %w", err)
	}
	detailsData, err := readLimited(detailsResponse)
	if err != nil {
		return err
	}
	if *dump != "" {
		file, err := os.OpenFile(*dump, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("private family dump must be a new file")
		}
		if _, err := file.Write(detailsData); err != nil || file.Close() != nil {
			return errors.New("cannot preserve private family response")
		}
	}
	details, err := decodeObject(detailsData)
	if err != nil {
		eligibilityRaw := stringValue(urls["checkFamilyEligibility"])
		if eligibilityRaw == "" {
			return errors.New("family details unavailable and eligibility endpoint missing")
		}
		eligibilityRequest, requestErr := newFamilyRequest(eligibilityRaw, false)
		if requestErr != nil {
			return requestErr
		}
		eligibilityResponse, requestErr := client.Do(eligibilityRequest)
		if requestErr != nil {
			return fmt.Errorf("family eligibility transport failed: %w", requestErr)
		}
		eligibilityData, requestErr := readLimited(eligibilityResponse)
		if requestErr != nil {
			return requestErr
		}
		eligibility, decodeErr := decodeObject(eligibilityData)
		if decodeErr != nil {
			return errors.New("family details and eligibility returned Apple UI errors")
		}
		keys := make([]string, 0, len(eligibility))
		for key := range eligibility {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		eligible, hasEligible := eligibility["eligible"].(bool)
		message := stringValue(eligibility["status-message"])
		if len(message) > 160 {
			message = message[:160]
		}
		fmt.Printf("family details unavailable; eligibility accepted; eligible-present=%t eligible=%t status-message=%q response-keys=%s; mutation-submitted=false\n", hasEligible, eligible, message, strings.Join(keys, ","))
		return nil
	}
	members, _ := details["family-members"].([]any)
	invites, _ := details["family-invitations"].([]any)
	pending, _ := details["pending-members"].([]any)
	organizers, children, me := 0, 0, 0
	for _, raw := range members {
		member, _ := raw.(map[string]any)
		if boolValue(member["member-is-organizer"]) {
			organizers++
		}
		if boolValue(member["member-is-child-account"]) {
			children++
		}
		if boolValue(member["is-me"]) {
			me++
		}
	}
	fmt.Printf("family details accepted; members=%d organizers=%d self=%d children=%d invitations=%d pending=%d; mutation-submitted=false\n", len(members), organizers, me, children, len(invites), len(pending))
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
