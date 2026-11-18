package app

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/fakephone"
	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/pairing"
	"github.com/jose00735/macawlink/windows/internal/vcam"
)

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestWiFiPairingAndReconnect(t *testing.T) {
	dir := t.TempDir()
	id, err := pairing.LoadOrCreateIdentity(dir, "TEST-PC")
	if err != nil {
		t.Fatal(err)
	}
	// Identity survives restarts.
	if id2, _ := pairing.LoadOrCreateIdentity(dir, "TEST-PC"); string(id2.Fingerprint) != string(id.Fingerprint) {
		t.Fatal("identity not persisted")
	}
	store := pairing.OpenStore(dir)
	addr := freeAddr(t)
	ring, _ := vcam.NewRingFromMemory(make([]byte, vcam.RingBytes), nil)
	var pmu sync.Mutex
	var pairCodes []string
	a := New(Options{
		Addr:        "127.0.0.1:1", // no USB phone in tests
		NewDecoder:  func(int) (native.Decoder, error) { return &fakeDecoder{}, nil },
		OpenAudio:   func(int, int) (native.AudioOut, error) { return &fakeAudio{}, nil },
		RingSources: []func() (*vcam.Ring, error){func() (*vcam.Ring, error) { return ring, nil }},
		WiFiAddr:    addr,
		Identity:    id,
		Pairs:       store,
		OnPairing:   func(phone, code string) { pmu.Lock(); pairCodes = append(pairCodes, code); pmu.Unlock() },
	}, config.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	waitFor(t, "listener", func() bool {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
		}
		return err == nil
	})

	opt := fakephone.Options{Model: "Pixel Wi-Fi", Settings: config.Default(), PhoneID: "phone-1",
		Confirm: func(string) bool { return true }}

	// 1) First connection: pairing with matching codes on both sides.
	rec := &fakephone.Received{}
	done := make(chan struct{})
	errc := make(chan error, 1)
	go func() { errc <- fakephone.DialWiFi(addr, opt, rec, done, nil) }()
	waitFor(t, "pairing prompt", func() bool { st := a.Status(); return st.Pairing != nil && st.Pairing.PhoneOK })
	pc := a.Status().Pairing.Code
	if code, _ := rec.Pairing(); pc != code || len(pc) != 6 {
		t.Fatalf("codes differ: pc %q phone %q", pc, code)
	}
	if err := a.PairDecision(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "wifi streaming", func() bool { st := a.Status(); return st.Phase == PhaseStreaming && st.Transport == "Wi-Fi" })
	_, token := rec.Pairing()
	if token == "" || len(a.PairedPhones()) != 1 || a.PairedPhones()[0].Name != "Fake Pixel Wi-Fi" {
		t.Fatalf("token=%q paired=%+v", token, a.PairedPhones())
	}
	if a.Status().WiFi.PCCode != id.ShortFP() {
		t.Fatal("pc code missing from status")
	}
	close(done)
	<-errc
	waitFor(t, "session end", func() bool { return a.Status().Phase != PhaseStreaming && !a.active.Load() })

	// 2) Reconnect with the token and the pinned certificate: no pairing prompt.
	opt.Token = token
	opt.Confirm = func(string) bool { t.Error("asked to pair again"); return false }
	done2 := make(chan struct{})
	go func() { errc <- fakephone.DialWiFi(addr, opt, &fakephone.Received{}, done2, id.Fingerprint) }()
	waitFor(t, "reconnect", func() bool { return a.Status().Phase == PhaseStreaming })
	if a.Status().Pairing != nil {
		t.Fatal("pairing shown for a paired phone")
	}
	close(done2)
	<-errc
	waitFor(t, "session end 2", func() bool { return !a.active.Load() })

	// 3) A different PC certificate (man in the middle) is refused by the phone.
	wrong := make([]byte, 32)
	if err := fakephone.DialWiFi(addr, opt, &fakephone.Received{}, make(chan struct{}), wrong); err == nil ||
		!strings.Contains(err.Error(), "certificate changed") {
		t.Fatalf("pinned mismatch accepted: %v", err)
	}

	// 4) A stranger with a stolen phone id but no token must pair; the PC user rejects.
	bad := fakephone.Options{Model: "Stranger", Settings: config.Default(), PhoneID: "phone-1", Token: "nope",
		Confirm: func(string) bool { return true }}
	go func() { errc <- fakephone.DialWiFi(addr, bad, &fakephone.Received{}, make(chan struct{}), nil) }()
	waitFor(t, "second pairing prompt", func() bool { return a.Status().Pairing != nil })
	a.PairDecision(false)
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("expected rejection, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rejected phone still connected")
	}
	if a.Status().Phase == PhaseStreaming {
		t.Fatal("rejected phone is streaming")
	}
	pmu.Lock()
	n := len(pairCodes)
	pmu.Unlock()
	if n != 2 {
		t.Fatalf("OnPairing calls = %d", n)
	}

	// Retrying right away from the same address gets no new prompt (cool-down).
	if err := fakephone.DialWiFi(addr, bad, &fakephone.Received{}, make(chan struct{}), nil); err == nil ||
		!strings.Contains(err.Error(), "declined recently") {
		t.Fatalf("cool-down not applied: %v", err)
	}

	// Forget removes it.
	a.ForgetPhone("phone-1")
	if len(a.PairedPhones()) != 0 || store.Check("phone-1", token) {
		t.Fatal("forget failed")
	}
}

// Plugging the cable in while streaming over Wi-Fi moves the session to USB by itself.
func TestWiFiToUSBUpgrade(t *testing.T) {
	dir := t.TempDir()
	id, _ := pairing.LoadOrCreateIdentity(dir, "TEST-PC")
	store := pairing.OpenStore(dir)
	token, _ := store.Add("phone-1", "Fake Pixel")
	wifiAddr, usbAddr := freeAddr(t), freeAddr(t) // nothing listens on usbAddr yet: cable unplugged
	ring, _ := vcam.NewRingFromMemory(make([]byte, vcam.RingBytes), nil)
	a := New(Options{
		Addr:        usbAddr,
		NewDecoder:  func(int) (native.Decoder, error) { return &fakeDecoder{}, nil },
		OpenAudio:   func(int, int) (native.AudioOut, error) { return &fakeAudio{}, nil },
		RingSources: []func() (*vcam.Ring, error){func() (*vcam.Ring, error) { return ring, nil }},
		WiFiAddr:    wifiAddr, Identity: id, Pairs: store,
	}, config.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	waitFor(t, "listener", func() bool {
		c, err := net.Dial("tcp", wifiAddr)
		if err == nil {
			c.Close()
		}
		return err == nil
	})

	opt := fakephone.Options{Model: "Pixel", Settings: config.Default(), PhoneID: "phone-1", Token: token}
	wifiDone := make(chan struct{})
	go fakephone.DialWiFi(wifiAddr, opt, nil, wifiDone, id.Fingerprint)
	waitFor(t, "wifi streaming", func() bool { return a.Status().Transport == "Wi-Fi" })

	// Cable goes in: the phone (Auto mode) leaves Wi-Fi and answers over USB.
	ln, err := net.Listen("tcp", usbAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	usbDone := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		close(wifiDone)                       // phone hangs up Wi-Fi first…
		fakephone.Serve(c, opt, nil, usbDone) // …then says HELLO over USB
	}()
	deadline := time.Now().Add(12 * time.Second)
	for a.Status().Transport != "TCP "+usbAddr || a.Status().Phase != PhaseStreaming {
		if time.Now().After(deadline) {
			t.Fatalf("still on %q (%s)", a.Status().Transport, a.Status().Phase)
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(usbDone)
}
