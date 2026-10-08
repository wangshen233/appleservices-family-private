// Command iclouddelegate reports why Apple refuses the iCloud delegate for an
// account. An iCloud+ storage purchase requires an account Apple recognises as
// iCloud-enabled (buyProduct answers failureType 3064 otherwise), and the
// delegate response is the only place that reason is stated.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/icloud"
)

type fileStore struct {
	dir    string
	device *appleservices.Device
	sess   *appleservices.Session
}

func (f fileStore) LoadDevice() (*appleservices.Device, error)   { return f.device, nil }
func (f fileStore) SaveDevice(*appleservices.Device) error       { return nil }
func (f fileStore) LoadSession() (*appleservices.Session, error) { return f.sess, nil }
func (f fileStore) SaveSession(*appleservices.Session) error     { return nil }
func (f fileStore) Path() string                                 { return f.dir }

func load(dir string) (fileStore, error) {
	s := fileStore{dir: dir}
	data, err := os.ReadFile(filepath.Join(dir, "device.json"))
	if err == nil {
		var dev appleservices.Device
		if json.Unmarshal(data, &dev) == nil {
			s.device = &dev
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	data, err = os.ReadFile(filepath.Join(dir, "session.json"))
	if err == nil {
		var sess appleservices.Session
		if json.Unmarshal(data, &sess) == nil {
			s.sess = &sess
		}
	}
	return s, nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: iclouddelegate <appleid> <password> [code] [dir]")
		os.Exit(2)
	}
	dir := "state"
	code := ""
	if len(os.Args) > 3 {
		code = os.Args[3]
	}
	if len(os.Args) > 4 {
		dir = os.Args[4]
	}
	store, err := load(dir)
	if err != nil {
		fmt.Println("state:", err)
		os.Exit(1)
	}
	login, err := appleservices.BeginLogin(appleservices.Credentials{AppleID: os.Args[1], Password: os.Args[2]}, store)
	if err != nil {
		fmt.Println("login:", err)
		os.Exit(1)
	}
	fmt.Println("needs two factor:", login.NeedsTwoFactor())
	if login.NeedsTwoFactor() {
		if code == "" {
			if err := login.RequestCode(appleservices.TrustedDevice); err != nil {
				fmt.Println("request code:", err)
			} else {
				fmt.Println("a trusted-device code was requested")
			}
			return
		}
		if err := login.SubmitCode(appleservices.TrustedDevice, code); err != nil {
			fmt.Println("submit code:", err)
			os.Exit(1)
		}
		fmt.Println("code accepted")
	}
	client, err := login.Client()
	if err != nil {
		fmt.Println("iCloud login FAILED:")
		fmt.Println("  ", err)
		os.Exit(1)
	}
	fmt.Println("iCloud login OK")
	fmt.Println("  DSID:        ", client.DSID)
	fmt.Println("  storage-buy: ", len(client.StorageBuyToken) > 0)
	settings, err := icloud.FetchAccountSettings(nil, client.DSID, client.ADSID, client.MMEAuthToken, nil)
	if err != nil {
		fmt.Println("account settings error:", err)
		return
	}
	fmt.Println("account settings fetched, keys:", len(settings))
}
