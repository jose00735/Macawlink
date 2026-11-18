// Package transport reaches the phone: over USB through adb port forwarding, or a direct
// TCP address (Wi-Fi manual mode / testing).
package transport

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jose00735/macawlink/windows/internal/protocol"
)

var ErrNoADB = errors.New("adb not found")

// Device is one entry from `adb devices -l`.
type Device struct {
	Serial string `json:"serial"`
	State  string `json:"state"` // device | unauthorized | offline
	Model  string `json:"model"`
}

// FindADB looks next to the executable first (the release bundles adb), then PATH,
// then the default Android SDK location.
func FindADB(exeDir string) (string, error) {
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	cands := []string{filepath.Join(exeDir, name), filepath.Join(exeDir, "platform-tools", name)}
	if p, err := exec.LookPath(name); err == nil {
		cands = append(cands, p)
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		cands = append(cands, filepath.Join(la, "Android", "Sdk", "platform-tools", name))
	}
	if h, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(h, "Android", "Sdk", "platform-tools", name))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", ErrNoADB
}

// ADB wraps the adb binary.
type ADB struct{ Path string }

func (a ADB) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Path, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("adb %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}

// Devices lists attached phones.
func (a ADB) Devices(ctx context.Context) ([]Device, error) {
	out, err := a.run(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	return ParseDevices(out), nil
}

// ParseDevices parses `adb devices -l` output.
func ParseDevices(out []byte) []Device {
	var ds []Device
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		d := Device{Serial: f[0], State: f[1]}
		for _, kv := range f[2:] {
			if strings.HasPrefix(kv, "model:") {
				d.Model = strings.ReplaceAll(strings.TrimPrefix(kv, "model:"), "_", " ")
			}
		}
		ds = append(ds, d)
	}
	return ds
}

// Forward maps localhost:port on the PC to the phone's localhost:port.
func (a ADB) Forward(ctx context.Context, serial string, port int) error {
	p := fmt.Sprintf("tcp:%d", port)
	_, err := a.run(ctx, "-s", serial, "forward", p, p)
	return err
}

// StartApp asks the phone to open Macawlink (handy when the user forgot).
func (a ADB) StartApp(ctx context.Context, serial string) error {
	_, err := a.run(ctx, "-s", serial, "shell", "am", "start", "-n", "com.macawlink/.ui.MainActivity",
		"--ez", "autostart", "true")
	return err
}

// Dial connects to addr and waits for the phone's HELLO. adb accepts forwarded
// connections even when nothing listens on the phone, so the HELLO is the real check.
func Dial(ctx context.Context, addr string) (net.Conn, protocol.Packet, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, protocol.Packet{}, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(5 * time.Second)
		tc.SetReadBuffer(1 << 20)
	}
	c.SetReadDeadline(time.Now().Add(6 * time.Second)) // a phone switching from Wi-Fi needs a moment
	p, err := protocol.Read(c)
	if err != nil {
		c.Close()
		return nil, protocol.Packet{}, fmt.Errorf("no HELLO from phone (is Macawlink streaming?): %w", err)
	}
	if p.Type == protocol.Bye {
		c.Close()
		return nil, protocol.Packet{}, fmt.Errorf("phone said bye: %s", p.Payload)
	}
	if p.Type != protocol.Hello {
		c.Close()
		return nil, protocol.Packet{}, fmt.Errorf("expected HELLO, got type 0x%02x", p.Type)
	}
	c.SetReadDeadline(time.Time{})
	return c, p, nil
}
