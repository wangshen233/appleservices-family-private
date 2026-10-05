package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Laky-64/appleservices"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintln(os.Stderr, "login: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, errOutput io.Writer) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(errOutput)
	appleID := flags.String("appleid", "", "Apple ID (or APPLESERVICES_APPLEID)")
	password := flags.String("password", "", "Apple ID password (prefer APPLESERVICES_PASSWORD)")
	dir := flags.String("dir", envOrDefault("APPLESERVICES_DIR", "./state"), "directory for device identity and trusted-session cookies")
	factor := flags.String("twofactor", "trusted-device", "verification method: trusted-device or sms")
	server := flags.String("anisette", "", "custom anisette server URL (default: public pool)")
	logout := flags.Bool("logout", false, "clear local trusted-session cookies and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if strings.TrimSpace(*dir) == "" {
		return fmt.Errorf("dir is required")
	}
	store := fileStore{dir: *dir}
	if *logout {
		if err := appleservices.Logout(store); err != nil {
			return err
		}
		fmt.Fprintln(output, "Local session cleared.")
		return nil
	}
	method := appleservices.TrustedDevice
	switch *factor {
	case "trusted-device":
	case "sms":
		method = appleservices.SMS
	default:
		return fmt.Errorf("twofactor must be trusted-device or sms")
	}
	if strings.TrimSpace(*appleID) == "" || *password == "" {
		if *appleID == "" {
			*appleID = os.Getenv("APPLESERVICES_APPLEID")
		}
		if *password == "" {
			*password = os.Getenv("APPLESERVICES_PASSWORD")
		}
	}
	if strings.TrimSpace(*appleID) == "" || *password == "" {
		return fmt.Errorf("Apple ID and password are required; use run-login.ps1 or APPLESERVICES_APPLEID/APPLESERVICES_PASSWORD")
	}
	var opts []appleservices.Option
	if *server != "" {
		opts = append(opts, appleservices.WithAnisetteServers(*server))
	}
	fmt.Fprintln(errOutput, "Authenticating Apple ID ...")
	login, err := appleservices.BeginLogin(appleservices.Credentials{AppleID: *appleID, Password: *password}, store, opts...)
	if err != nil {
		return err
	}
	if login.NeedsTwoFactor() {
		fmt.Fprintln(errOutput, "Two-factor authentication required; requesting a code ...")
		if err := login.RequestCode(method); err != nil {
			return err
		}
		fmt.Fprint(errOutput, "Enter the six-digit verification code: ")
		code, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && (err != io.EOF || code == "") {
			return fmt.Errorf("read verification code: %w", err)
		}
		if err := login.SubmitCode(method, code); err != nil {
			return err
		}
	}
	fmt.Fprintln(errOutput, "Completing iCloud login ...")
	if _, err := login.Client(); err != nil {
		return err
	}
	// Do not print account IDs, credentials, cookies, or tokens.
	fmt.Fprintln(output, "iCloud login successful. Trusted session saved.")
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
