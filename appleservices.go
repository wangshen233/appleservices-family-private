package appleservices

import (
	"errors"
	"fmt"

	"github.com/Laky-64/appleservices/gsa"
)

type Store interface {
	LoadDevice() (*Device, error)
	SaveDevice(*Device) error
	LoadSession() (*Session, error)
	SaveSession(*Session) error
}

func Logout(store Store) error {
	if store == nil {
		return errors.New("appleservices: Logout requires a Store")
	}
	if err := store.SaveSession(nil); err != nil {
		return fmt.Errorf("appleservices: Logout: clear session: %w", err)
	}
	return nil
}

type Device struct {
	Identifier       []byte
	ProvisioningBlob []byte
}

type Session struct {
	AppleID string
	DSID    string
	Cookies []Cookie
}

type Cookie struct {
	URL   string
	Name  string
	Value string
}

type Credentials struct {
	AppleID  string
	Password string
}

type Option func(*options)

type options struct {
	anisette      gsa.AnisetteProvider
	newBackend    backendFactory
	fetchDelegate delegateFetcher
	servers       []string
}

func WithAnisette(p gsa.AnisetteProvider) Option {
	return func(o *options) {
		o.anisette = p
	}
}

// WithAnisetteServers uses the supplied anisette servers instead of the public pool.
func WithAnisetteServers(urls ...string) Option {
	return func(o *options) {
		o.servers = append([]string{}, urls...)
	}
}
