package gsa

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

type failedProvider struct{}

func (failedProvider) Headers() (map[string]string, error) {
	return nil, errors.New("test unavailable")
}

func TestRefreshRejectsStaleHeaders(t *testing.T) {
	c := NewClient(failedProvider{})
	c.cachedAnisetteHeaders = map[string]string{"X-Apple-I-MD": "test-stale"}
	if _, err := c.refreshAnisetteHeaders(); err == nil {
		t.Fatal("used stale headers after failed refresh")
	}
}

func TestSnapshotRestoresRootCookieForAuthAndLogin(t *testing.T) {
	c := NewClient(failedProvider{})
	c.cachedAnisetteHeaders = map[string]string{}
	u, _ := url.Parse(gsaBaseURL + "/auth")
	c.cookieJar.SetCookies(u, []*http.Cookie{{Name: "test-cookie", Value: "test-value", Path: "/", Secure: true}})
	sess, err := c.Snapshot("test-dsid", nil)
	if err != nil {
		t.Fatal(err)
	}
	restored := NewClientFromSession(failedProvider{}, sess)
	for _, endpoint := range []string{"/auth", "/auth/verify/trusteddevice", "/grandslam/GsService2"} {
		u, _ := url.Parse(gsaBaseURL + endpoint)
		cookies := restored.cookieJar.Cookies(u)
		if len(cookies) != 1 || cookies[0].Value != "test-value" {
			t.Fatalf("cookie not restored for %s", endpoint)
		}
	}
}
