package app

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/jose00735/macawlink/windows/internal/pairing"
	"github.com/jose00735/macawlink/windows/internal/protocol"
	"github.com/jose00735/macawlink/windows/internal/transport"
)

// WiFiStatus is shown on the settings page (and used for manual connection).
type WiFiStatus struct {
	Enabled   bool     `json:"enabled"`
	Port      int      `json:"port"`
	Addresses []string `json:"addresses"`
	PCCode    string   `json:"pc_code"` // short certificate fingerprint, also in mDNS TXT
	Error     string   `json:"error,omitempty"`
}

// PairingStatus is non-nil while a phone waits to be paired.
type PairingStatus struct {
	Phone   string    `json:"phone"`
	Code    string    `json:"code"`
	PhoneOK bool      `json:"phone_ok"` // the phone user already confirmed
	Since   time.Time `json:"since"`
}

type pairingState struct {
	status   PairingStatus
	decision chan bool
}

const (
	pairTimeout  = 2 * time.Minute
	pairCooldown = 20 * time.Second
)

func (a *App) idleHint() string {
	if a.opt.WiFiAddr != "" {
		return "Plug in your phone by USB, or tap Connect over Wi-Fi in the Macawlink app"
	}
	return "Plug in your phone by USB (USB debugging on)"
}

func (a *App) wifiLoop(ctx context.Context) {
	_, port, _ := net.SplitHostPort(a.opt.WiFiAddr)
	a.mu.Lock()
	a.status.WiFi = WiFiStatus{Enabled: true, PCCode: a.opt.Identity.ShortFP(), Addresses: localIPv4s()}
	fmt.Sscan(port, &a.status.WiFi.Port)
	a.mu.Unlock()

	ln, err := tls.Listen("tcp", a.opt.WiFiAddr, a.opt.Identity.TLSConfig())
	if err != nil {
		a.mu.Lock()
		a.status.WiFi.Error = err.Error()
		a.mu.Unlock()
		log.Printf("wifi: %v", err)
		return
	}
	log.Printf("wifi: listening on %s (PC code %s)", ln.Addr(), a.opt.Identity.ShortFP())
	go func() { <-ctx.Done(); ln.Close() }()
	go func() { // addresses change when the PC switches networks
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.mu.Lock()
				a.status.WiFi.Addresses = localIPv4s()
				a.mu.Unlock()
			}
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("wifi: accept: %v", err)
				time.Sleep(time.Second)
				continue
			}
			return
		}
		go a.handleWiFi(ctx, c)
	}
}

func (a *App) handleWiFi(ctx context.Context, c net.Conn) {
	remote := c.RemoteAddr().String()
	if tc, ok := c.(*tls.Conn); ok {
		c.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.HandshakeContext(ctx); err != nil {
			log.Printf("wifi: TLS handshake with %s: %v", remote, err)
			c.Close()
			return
		}
		if raw, ok := tc.NetConn().(*net.TCPConn); ok {
			raw.SetNoDelay(true)
			raw.SetKeepAlive(true)
			raw.SetKeepAlivePeriod(5 * time.Second)
		}
	}
	helloPkt, err := protocol.Read(c)
	if err != nil || helloPkt.Type != protocol.Hello {
		c.Close()
		return
	}
	var hello protocol.HelloMsg
	if json.Unmarshal(helloPkt.Payload, &hello) != nil || hello.PhoneID == "" {
		bye(c, "missing phone id")
		return
	}
	if a.active.Load() {
		bye(c, "busy: another phone is streaming")
		return
	}
	name := strings.TrimSpace(hello.Manufacturer + " " + hello.Model)
	ip, _, _ := net.SplitHostPort(remote)
	if !a.opt.Pairs.Check(hello.PhoneID, hello.Token) {
		// Unknown phone: pairing prompt, but don't let one address spam the PC with prompts.
		a.mu.Lock()
		if a.pairFail == nil {
			a.pairFail = map[string]time.Time{}
		}
		last := a.pairFail[ip]
		a.mu.Unlock()
		if time.Since(last) < pairCooldown {
			bye(c, "pairing was declined recently; try again in a moment")
			return
		}
		c.SetDeadline(time.Now().Add(pairTimeout + 10*time.Second))
		if err := a.pair(ctx, c, hello.PhoneID, name); err != nil {
			log.Printf("wifi: pairing with %s failed: %v", name, err)
			a.mu.Lock()
			a.pairFail[ip] = time.Now()
			a.mu.Unlock()
			c.Close()
			return
		}
	}
	c.SetDeadline(time.Time{})
	if !a.active.CompareAndSwap(false, true) {
		bye(c, "busy: another phone is streaming")
		return
	}
	host, _, _ := net.SplitHostPort(remote)
	log.Printf("wifi: %s connected from %s", name, host)
	a.runSession(ctx, c, helloPkt, transport.Device{Serial: host, Model: hello.Model}, "Wi-Fi")
}

// pair runs numeric comparison: both screens show the same 6-digit code; both users confirm.
func (a *App) pair(ctx context.Context, c net.Conn, phoneID, phoneName string) error {
	st := &pairingState{decision: make(chan bool, 1)}
	a.mu.Lock()
	if a.pairing != nil {
		a.mu.Unlock()
		bye(c, "another pairing is in progress")
		return errors.New("busy")
	}
	a.pairing = st
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.pairing == st {
			a.pairing = nil
		}
		a.mu.Unlock()
	}()

	host, _ := os.Hostname()
	pcNonce := pairing.NewNonce()
	if err := writeJSON(c, protocol.PairBegin, protocol.PairBeginMsg{Commit: protocol.Commit(pcNonce), PCName: host}); err != nil {
		return err
	}
	p, err := protocol.Read(c)
	if err != nil {
		return err
	}
	var pn protocol.PairNonceMsg
	if p.Type != protocol.PairNonce || json.Unmarshal(p.Payload, &pn) != nil {
		return errors.New("expected PAIR_NONCE")
	}
	phoneNonce, err := hex.DecodeString(pn.Nonce)
	if err != nil || len(phoneNonce) < 16 {
		return errors.New("bad phone nonce")
	}
	if err := writeJSON(c, protocol.PairReveal, protocol.PairNonceMsg{Nonce: hex.EncodeToString(pcNonce)}); err != nil {
		return err
	}
	code := protocol.SAS(a.opt.Identity.Fingerprint, pcNonce, phoneNonce)
	a.mu.Lock()
	st.status = PairingStatus{Phone: phoneName, Code: code, Since: time.Now()}
	a.mu.Unlock()
	log.Printf("wifi: pairing request from %s, code %s", phoneName, code)
	if a.opt.OnPairing != nil {
		go a.opt.OnPairing(phoneName, code)
	}

	phoneOK := make(chan bool, 1)
	go func() {
		p, err := protocol.Read(c)
		var pc protocol.PairConfirmMsg
		if err != nil || p.Type != protocol.PairConfirm || json.Unmarshal(p.Payload, &pc) != nil {
			phoneOK <- false
			return
		}
		phoneOK <- pc.OK
	}()

	gotPhone, gotPC := false, false
	timeout := time.After(pairTimeout)
	for !(gotPhone && gotPC) {
		select {
		case ok := <-phoneOK:
			if !ok {
				return errors.New("rejected on the phone")
			}
			gotPhone = true
			a.mu.Lock()
			st.status.PhoneOK = true
			a.mu.Unlock()
		case ok := <-st.decision:
			if !ok {
				writeJSON(c, protocol.PairResult, protocol.PairResultMsg{OK: false, Reason: "rejected on the PC"})
				return errors.New("rejected on the PC")
			}
			gotPC = true
		case <-timeout:
			writeJSON(c, protocol.PairResult, protocol.PairResultMsg{OK: false, Reason: "timed out"})
			return errors.New("timed out")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	token, err := a.opt.Pairs.Add(phoneID, phoneName)
	if err != nil {
		return err
	}
	log.Printf("wifi: paired %s", phoneName)
	return writeJSON(c, protocol.PairResult, protocol.PairResultMsg{OK: true, Token: token})
}

// PairDecision is the PC user's answer to the pending pairing (settings page / tray).
func (a *App) PairDecision(accept bool) error {
	a.mu.Lock()
	st := a.pairing
	a.mu.Unlock()
	if st == nil || st.status.Code == "" {
		return errors.New("no pairing in progress")
	}
	select {
	case st.decision <- accept:
	default:
	}
	return nil
}

func (a *App) PairedPhones() []pairing.Phone {
	if a.opt.Pairs == nil {
		return nil
	}
	return a.opt.Pairs.List()
}

func (a *App) ForgetPhone(id string) {
	if a.opt.Pairs != nil {
		a.opt.Pairs.Remove(id)
	}
}

func writeJSON(c net.Conn, t protocol.Type, v any) error {
	b, _ := json.Marshal(v)
	c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return protocol.Write(c, protocol.Packet{Type: t, Payload: b})
}

func bye(c net.Conn, reason string) {
	b, _ := json.Marshal(protocol.ByeMsg{Reason: reason})
	c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	protocol.Write(c, protocol.Packet{Type: protocol.Bye, Payload: b})
	c.Close()
}

func localIPv4s() []string {
	ifs, _ := net.Interfaces()
	var out []string
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				if ip := ipn.IP.To4(); ip != nil && ip.IsPrivate() {
					out = append(out, ip.String())
				}
			}
		}
	}
	return out
}
