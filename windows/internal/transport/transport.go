// Package transport gets a byte stream to the phone, over USB (adb forward) or Wi-Fi (TLS).
package transport

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"time"

	"github.com/jose00735/macawlink/windows/internal/protocol"
)

// Mode selects how the phone is reached.
type Mode int

const (
	USB Mode = iota
	WiFi
)

// DialUSB forwards the phone's listening port through adb and connects to it.
// adb.exe is bundled next to macawlink.exe by the installer.
func DialUSB(ctx context.Context, adbPath, serial string) (net.Conn, error) {
	port := fmt.Sprint(protocol.DefaultPort)
	args := []string{}
	if serial != "" {
		args = append(args, "-s", serial)
	}
	args = append(args, "forward", "tcp:"+port, "tcp:"+port)
	if out, err := exec.CommandContext(ctx, adbPath, args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("adb forward: %v: %s", err, out)
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", "127.0.0.1:"+port)
	if err != nil {
		return nil, err
	}
	tuneTCP(c)
	return c, nil
}

// ListenWiFi accepts paired phones over TLS 1.3 (see pairing in docs/ARCHITECTURE.md §6).
// TODO(phase2): wrap with tls.NewListener using the PC's pinned certificate.
func ListenWiFi(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// tuneTCP disables Nagle so small audio packets are not delayed.
func tuneTCP(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(5 * time.Second)
	}
}
