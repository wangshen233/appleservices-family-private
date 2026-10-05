package aps

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

//go:embed native_push.cs
var nativeBridge string

// NativeClient uses the installed iCloud APSDaemon COM service. The daemon owns
// its certificate and private key; they are never exported to this program.
type NativeClient struct {
	mu       sync.Mutex
	command  *exec.Cmd
	input    io.WriteCloser
	token    []byte
	ready    chan nativeReady
	events   chan Event
	selftest chan struct{}
	done     chan struct{}
	finalErr error
	probe    bool
	closed   bool
}

type nativeReady struct {
	token []byte
	err   error
}

type nativeMessage struct {
	Type    string `json:"type"`
	Token   []byte `json:"token"`
	Payload []byte `json:"payload"`
	Stage   string `json:"stage"`
	HRESULT string `json:"hresult"`
}

func NewNativeClient() (*NativeClient, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("aps: native push requires Windows with iCloud installed")
	}
	return &NativeClient{ready: make(chan nativeReady, 1), events: make(chan Event, 16),
		selftest: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// ProbeNativePush verifies the real daemon token and a private local COM
// callback. It does not contact account APIs or prove remote notification delivery.
func ProbeNativePush(ctx context.Context) (int, error) {
	client, err := NewNativeClient()
	if err != nil {
		return 0, err
	}
	client.probe = true
	if err := client.Connect(ctx); err != nil {
		return 0, err
	}
	defer client.Close()
	select {
	case <-client.selftest:
		return len(client.State().Token), nil
	case <-client.done:
		return 0, errors.New("aps: native bridge ended before the callback probe")
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (c *NativeClient) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{Token: append([]byte(nil), c.token...)}
}

func nativeCommand(probe bool) (*exec.Cmd, error) {
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		return nil, errors.New("aps: Windows system directory is unavailable")
	}
	flag := "$false"
	if probe {
		flag = "$true"
	}
	script := "$ErrorActionPreference='Stop'\nAdd-Type -TypeDefinition @'\n" + nativeBridge +
		"\n'@\n[AppleServicesNativePush.Bridge]::Run(" + flag + ")\n"
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	// Only embedded program text goes to PowerShell. No credentials, notification
	// contents, provider paths, or user supplied shell expressions are arguments.
	command := exec.Command(filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-Mta", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "APPLESERVICES_APPLEID") && !strings.EqualFold(key, "APPLESERVICES_PASSWORD") {
			command.Env = append(command.Env, entry)
		}
	}
	return command, nil
}

func (c *NativeClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.command != nil || c.closed {
		c.mu.Unlock()
		return errors.New("aps: native push client cannot be opened again")
	}
	command, err := nativeCommand(c.probe)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	input, err := command.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return errors.New("aps: native bridge input unavailable")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		c.mu.Unlock()
		return errors.New("aps: native bridge output unavailable")
	}
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		c.mu.Unlock()
		return errors.New("aps: native push bridge could not start")
	}
	c.command, c.input = command, input
	c.mu.Unlock()
	go c.read(output)
	select {
	case result := <-c.ready:
		if result.err != nil {
			c.Close()
			return result.err
		}
		c.mu.Lock()
		c.token = result.token
		c.mu.Unlock()
		return nil
	case <-ctx.Done():
		c.Close()
		return ctx.Err()
	}
}

func nativeBridgeError(message nativeMessage) error {
	// Only known local diagnostic fields are included. Never relay arbitrary
	// subprocess text or the notification payload in a user facing error.
	stages := map[string]bool{"create-service": true, "initialize": true, "token": true,
		"notification-center": true, "callback": true, "subscribe": true, "payload-size": true}
	if !stages[message.Stage] {
		return errors.New("aps: native push bridge failed")
	}
	if len(message.HRESULT) == 8 {
		for _, digit := range message.HRESULT {
			if !(digit >= '0' && digit <= '9' || digit >= 'A' && digit <= 'F') {
				return errors.New("aps: native push bridge failed")
			}
		}
		return fmt.Errorf("aps: native push %s failed (HRESULT 0x%s)", message.Stage, message.HRESULT)
	}
	return fmt.Errorf("aps: native push %s failed", message.Stage)
}

func (c *NativeClient) read(output io.Reader) {
	var failure error
	ready := false
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	for scanner.Scan() {
		var message nativeMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			failure = errors.New("aps: native bridge returned invalid data")
			break
		}
		switch message.Type {
		case "ready":
			if ready || len(message.Token) != 32 {
				failure = errors.New("aps: native bridge returned an invalid push token")
				break
			}
			ready = true
			c.ready <- nativeReady{token: message.Token}
		case "event":
			if !ready {
				failure = errors.New("aps: native notification arrived before initialization")
				break
			}
			event, err := ParseEvent(message.Payload)
			if errors.Is(err, ErrUnhandledNotice) {
				continue
			}
			if err != nil {
				failure = err
				break
			}
			select {
			case c.events <- event:
			default:
				failure = errors.New("aps: native notification queue is full")
			}
		case "selftest":
			if !c.probe {
				failure = errors.New("aps: unexpected native probe result")
				break
			}
			select {
			case c.selftest <- struct{}{}:
			default:
			}
		case "error":
			failure = nativeBridgeError(message)
		default:
			failure = errors.New("aps: unknown native bridge message")
		}
		if failure != nil {
			break
		}
	}
	if scanner.Err() != nil && failure == nil {
		failure = errors.New("aps: native bridge output failed or exceeded size limit")
	}
	// Closing stdin asks the helper to remove only its topic and unadvise its
	// callback. Close() kills a hung helper after a bounded graceful shutdown.
	c.input.Close()
	if err := c.command.Wait(); err != nil && failure == nil {
		failure = errors.New("aps: native bridge exited with an error")
	}
	if !ready {
		if failure == nil {
			failure = errors.New("aps: native bridge did not initialize")
		}
		c.ready <- nativeReady{err: failure}
	}
	c.mu.Lock()
	c.finalErr = failure
	c.mu.Unlock()
	close(c.done)
}

func (c *NativeClient) Listen(ctx context.Context, handle func(Event) error) error {
	if handle == nil {
		return errors.New("aps: event handler is required")
	}
	if len(c.State().Token) != 32 {
		return errors.New("aps: native push is not connected")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			c.mu.Lock()
			err := c.finalErr
			c.mu.Unlock()
			if err == nil {
				err = errors.New("aps: native push connection closed")
			}
			return err
		case event := <-c.events:
			if err := handle(event); err != nil {
				return err
			}
		}
	}
}

func (c *NativeClient) Close() error {
	c.mu.Lock()
	c.closed = true
	command, input := c.command, c.input
	c.mu.Unlock()
	if command == nil {
		return nil
	}
	input.Close()
	select {
	case <-c.done:
		return nil
	case <-time.After(5 * time.Second):
		command.Process.Kill()
		return errors.New("aps: native bridge shutdown timed out")
	}
}
