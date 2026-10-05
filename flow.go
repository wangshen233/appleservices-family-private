package appleservices

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Laky-64/appleservices/anisette"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/icloud"
)

type loginBackend interface {
	Login(username, password string) (*gsa.LoginResult, error)
	RequestTrustedDeviceCode(dsid, idmsToken string) error
	SubmitTrustedDeviceCode(dsid, idmsToken, code string) error
	RequestSMSCode(dsid, idmsToken string) error
	SubmitSMSCode(dsid, idmsToken, code string) error
	Snapshot(dsid string, tokens map[string]string) (gsa.Session, error)
}

type backendFactory func(gsa.AnisetteProvider, *gsa.Session) loginBackend
type delegateFetcher func(map[string]string, string, string, string) (icloud.DelegateTokens, error)

func defaultBackend(p gsa.AnisetteProvider, sess *gsa.Session) loginBackend {
	if sess != nil {
		return gsa.NewClientFromSession(p, *sess)
	}
	return gsa.NewClient(p)
}

// Login handles Apple ID authentication and any pending two-factor challenge.
// Call Client to complete iCloud login; no account data is fetched.
type Login struct {
	creds          Credentials
	store          Store
	backend        loginBackend
	anisette       gsa.AnisetteProvider
	stateful       *anisette.Provider
	fetchDelegate  delegateFetcher
	needsTwoFactor bool
	result         *gsa.LoginResult
	adsid          string
	idmsToken      string
	client         *Client
}

func BeginLogin(creds Credentials, store Store, opts ...Option) (*Login, error) {
	if store == nil {
		return nil, errors.New("appleservices: store is required")
	}
	creds.AppleID = strings.TrimSpace(creds.AppleID)
	if creds.AppleID == "" || creds.Password == "" {
		return nil, errors.New("appleservices: Apple ID and password are required")
	}
	o := options{newBackend: defaultBackend, fetchDelegate: icloud.FetchDelegateTokens}
	for _, opt := range opts {
		opt(&o)
	}
	l := &Login{creds: creds, store: store, fetchDelegate: o.fetchDelegate}
	if o.anisette != nil {
		l.anisette = o.anisette
	} else {
		dev, err := store.LoadDevice()
		if err != nil {
			return nil, fmt.Errorf("appleservices: load device: %w", err)
		}
		st := anisette.NewState()
		if dev != nil && len(dev.Identifier) == 16 {
			st = anisette.State{Identifier: dev.Identifier, AdiPB: dev.ProvisioningBlob}
		}
		l.stateful = anisette.NewProviderFromState(st, o.servers)
		l.anisette = l.stateful
	}
	sess, err := store.LoadSession()
	if err != nil {
		return nil, fmt.Errorf("appleservices: load session: %w", err)
	}
	var gsess *gsa.Session
	// Older, unbound sessions and sessions belonging to another account are ignored.
	if sess != nil && strings.EqualFold(sess.AppleID, creds.AppleID) {
		s := toGSASession(*sess)
		gsess = &s
	}
	l.backend = o.newBackend(l.anisette, gsess)
	// Cached cookies may avoid a new 2FA prompt, but never establish login on their own.
	if err := l.authenticate(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Login) persistDevice() error {
	if l.stateful == nil {
		return nil
	}
	st := l.stateful.State()
	if err := l.store.SaveDevice(&Device{Identifier: st.Identifier, ProvisioningBlob: st.AdiPB}); err != nil {
		return fmt.Errorf("appleservices: save device: %w", err)
	}
	return nil
}

func (l *Login) authenticate() error {
	res, loginErr := l.backend.Login(l.creds.AppleID, l.creds.Password)
	// Provisioning can change even when account authentication fails.
	saveErr := l.persistDevice()
	if loginErr != nil {
		return errors.Join(fmt.Errorf("appleservices: login: %w", loginErr), saveErr)
	}
	if saveErr != nil {
		return saveErr
	}
	if res == nil || res.SessionPayload == nil {
		return errors.New("appleservices: login returned no session payload")
	}
	adsid := spdString(res.SessionPayload, "adsid")
	idmsToken := spdString(res.SessionPayload, "GsIdmsToken")
	if adsid == "" {
		return errors.New("appleservices: login returned no account ID")
	}
	if res.NeedsTwoFactor && idmsToken == "" {
		return errors.New("appleservices: two-factor challenge missing GsIdmsToken")
	}
	l.result = res
	l.adsid, l.idmsToken = adsid, idmsToken
	l.needsTwoFactor = res.NeedsTwoFactor
	l.client = nil
	return nil
}

func (l *Login) NeedsTwoFactor() bool { return l.needsTwoFactor }

type TwoFactorMethod int

const (
	TrustedDevice TwoFactorMethod = iota
	SMS
)

func (l *Login) RequestCode(method TwoFactorMethod) error {
	if !l.needsTwoFactor {
		return errors.New("appleservices: no two-factor challenge is pending")
	}
	switch method {
	case TrustedDevice:
		return l.backend.RequestTrustedDeviceCode(l.adsid, l.idmsToken)
	case SMS:
		return l.backend.RequestSMSCode(l.adsid, l.idmsToken)
	default:
		return fmt.Errorf("appleservices: unknown two-factor method %d", method)
	}
}

func (l *Login) SubmitCode(method TwoFactorMethod, code string) error {
	if !l.needsTwoFactor {
		return errors.New("appleservices: no two-factor challenge is pending")
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return errors.New("appleservices: verification code must contain six digits")
	}
	var err error
	switch method {
	case TrustedDevice:
		err = l.backend.SubmitTrustedDeviceCode(l.adsid, l.idmsToken, code)
	case SMS:
		err = l.backend.SubmitSMSCode(l.adsid, l.idmsToken, code)
	default:
		return fmt.Errorf("appleservices: unknown two-factor method %d", method)
	}
	if err != nil {
		return fmt.Errorf("appleservices: submit code: %w", err)
	}
	if verified, ok := l.backend.(interface{ VerifiedLoginResult() *gsa.LoginResult }); ok {
		if result := verified.VerifiedLoginResult(); result != nil {
			if result.NeedsTwoFactor || spdString(result.SessionPayload, "adsid") != l.adsid {
				return errors.New("appleservices: verification returned an invalid account session")
			}
			if _, err := icloud.PETFromSPD(result.SessionPayload); err != nil {
				return err
			}
			l.result = result
			l.needsTwoFactor = false
			return l.persistDevice()
		}
	}
	if err := l.authenticate(); err != nil {
		return err
	}
	if l.needsTwoFactor {
		return errors.New("appleservices: still prompted for 2FA after submitting a code")
	}
	return nil
}

// Client is the result of a completed iCloud login. Tokens are sensitive.
// This login-only client has no data retrieval or modification methods.
type Client struct {
	DSID          string
	ADSID         string
	MMEAuthToken  string
	CloudKitToken string
}

// Client exchanges the authenticated Apple ID session for iCloud tokens.
// A session is saved only after iCloud accepts the login, with or without 2FA.
func (l *Login) Client() (*Client, error) {
	if l.needsTwoFactor {
		return nil, errors.New("appleservices: two-factor authentication required; submit a code first")
	}
	if l.client != nil {
		return l.client, nil
	}
	pet, err := icloud.PETFromSPD(l.result.SessionPayload)
	if err != nil {
		return nil, err
	}
	headers, headerErr := l.anisette.Headers()
	saveErr := l.persistDevice()
	if headerErr != nil {
		return nil, errors.Join(fmt.Errorf("appleservices: anisette headers: %w", headerErr), saveErr)
	}
	if saveErr != nil {
		return nil, saveErr
	}
	dt, err := l.fetchDelegate(headers, l.creds.AppleID, l.adsid, pet)
	if err != nil {
		return nil, fmt.Errorf("appleservices: iCloud login: %w", err)
	}
	if dt.DSID == "" || dt.MMEAuthToken == "" {
		return nil, errors.New("appleservices: iCloud login returned no account ID or auth token")
	}
	gsess, err := l.backend.Snapshot(l.adsid, nil)
	if err != nil {
		return nil, fmt.Errorf("appleservices: snapshot session: %w", err)
	}
	sess := fromGSASession(gsess)
	sess.AppleID = l.creds.AppleID
	if err := l.store.SaveSession(&sess); err != nil {
		return nil, fmt.Errorf("appleservices: save session: %w", err)
	}
	l.client = &Client{DSID: dt.DSID, ADSID: l.adsid, MMEAuthToken: dt.MMEAuthToken, CloudKitToken: dt.CloudKitToken}
	return l.client, nil
}

func toGSASession(s Session) gsa.Session {
	cookies := map[string][]gsa.CookieKV{}
	for _, c := range s.Cookies {
		cookies[c.URL] = append(cookies[c.URL], gsa.CookieKV{Name: c.Name, Value: c.Value})
	}
	return gsa.Session{DSID: s.DSID, Cookies: cookies}
}

func fromGSASession(g gsa.Session) Session {
	var cookies []Cookie
	for url, kvs := range g.Cookies {
		for _, kv := range kvs {
			cookies = append(cookies, Cookie{URL: url, Name: kv.Name, Value: kv.Value})
		}
	}
	return Session{DSID: g.DSID, Cookies: cookies}
}

func spdString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
