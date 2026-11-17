//go:build windows

// Command macawlink is the Windows receiver: it makes an Android phone appear as
// "Macawlink Camera" and feeds its microphone into a virtual audio device.
// See docs/ARCHITECTURE.md.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/jose00735/macawlink/windows/internal/app"
	"github.com/jose00735/macawlink/windows/internal/audio"
	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/install"
	"github.com/jose00735/macawlink/windows/internal/mdns"
	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/pairing"
	"github.com/jose00735/macawlink/windows/internal/protocol"
	"github.com/jose00735/macawlink/windows/internal/tray"
	"github.com/jose00735/macawlink/windows/internal/vcam"
	"github.com/jose00735/macawlink/windows/internal/web"
)

//go:embed macawlink.ico
var iconICO []byte

func init() { runtime.LockOSThread() } // the tray message loop must stay on the main thread

func main() {
	doInstall := flag.Bool("install", false, "one-time setup (admin): install to Program Files and register the camera")
	doUninstall := flag.Bool("uninstall", false, "remove the camera registration and installed files (admin)")
	addr := flag.String("addr", "", "connect to host:port directly instead of over USB/adb (e.g. a fake phone)")
	serial := flag.String("serial", "", "adb serial of the phone to use when several are plugged in")
	webAddr := flag.String("web", web.DefaultAddr, "address of the local settings page")
	wifi := flag.Bool("wifi", true, "accept phones over Wi-Fi (TLS, paired with a code)")
	flag.Parse()

	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	logFile := setupLog(*doInstall || *doUninstall)
	log.Printf("Macawlink %s starting from %s", app.Version, exeDir)

	switch {
	case *doInstall:
		if err := install.Install(exeDir); err != nil {
			log.Printf("install: %v", err)
			install.MessageBox("Macawlink setup", "Setup failed:\n\n"+err.Error(), install.MBIconError)
			os.Exit(1)
		}
		log.Printf("installed to %s", install.Dir())
		os.Exit(0)
	case *doUninstall:
		if err := install.Uninstall(exeDir); err != nil {
			install.MessageBox("Macawlink", "Uninstall failed:\n\n"+err.Error(), install.MBIconError)
			os.Exit(1)
		}
		install.MessageBox("Macawlink", "Macawlink Camera was removed.", install.MBIconInfo)
		os.Exit(0)
	}

	url := "http://" + *webAddr + "/"
	if !singleInstance() {
		openURL(url) // already running: just bring up the settings page
		return
	}

	if err := native.Load(exeDir); err != nil {
		install.MessageBox("Macawlink", fmt.Sprintf("%s is missing or broken next to macawlink.exe:\n\n%v", native.DLLName, err), install.MBIconError)
		os.Exit(1)
	}

	cam := setupCamera(exeDir)
	defer onWorker(native.StopVirtualCamera)

	settings, err := config.Load()
	if err != nil {
		log.Printf("settings: %v (using defaults)", err)
	}
	// Wi-Fi: TLS identity, paired phones and mDNS advertisement.
	var lastPairOpen time.Time
	var identity *pairing.Identity
	var pairs *pairing.Store
	wifiAddr := ""
	notify := make(chan [2]string, 4)
	host, _ := os.Hostname()
	if *wifi {
		cfgDir, _ := config.Dir()
		if identity, err = pairing.LoadOrCreateIdentity(cfgDir, host); err != nil {
			log.Printf("wifi disabled: %v", err)
		} else {
			pairs = pairing.OpenStore(cfgDir)
			wifiAddr = fmt.Sprintf(":%d", protocol.WiFiPort)
		}
	}

	a := app.New(app.Options{
		ExeDir:     exeDir,
		Addr:       *addr,
		Serial:     *serial,
		NewDecoder: native.NewDecoder,
		OpenAudio: func(rate, ch int) (native.AudioOut, error) {
			return native.OpenAudio(audio.Endpoints, rate, ch)
		},
		// Windows 11: ring created by Frame Server. Windows 10: our session ring for the
		// DirectShow camera inside each app. Opening both is harmless.
		RingSources: []func() (*vcam.Ring, error){vcam.OpenGlobal, vcam.CreateLocal},
		Camera:      cam,
		SaveConfig:  true,
		WiFiAddr:    wifiAddr,
		Identity:    identity,
		Pairs:       pairs,
		OnPairing: func(phone, code string) {
			notify <- [2]string{"Pair " + phone + "?", "Code " + code[:3] + " " + code[3:] + " — accept on the settings page if the phone shows the same code."}
			if time.Since(lastPairOpen) > time.Minute { // don't keep popping browser tabs
				lastPairOpen = time.Now()
				openURL(url)
			}
		},
	}, settings)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()

	ln, err := net.Listen("tcp", *webAddr)
	if err != nil {
		log.Printf("settings page: %v", err)
	} else {
		srv := &http.Server{Handler: web.Handler(a), ReadHeaderTimeout: 5 * time.Second}
		go srv.Serve(ln)
		defer srv.Close()
		log.Printf("settings page on %s", url)
	}

	if identity != nil {
		md := mdns.Start(mdns.Service{
			Instance: "Macawlink on " + host,
			Host:     mdns.HostLabel(host),
			Port:     protocol.WiFiPort,
			TXT:      map[string]string{"v": "1", "name": host, "fp": identity.ShortFP()},
		})
		defer md.Stop()
	}
	go watchPhase(ctx, a, notify)

	err = tray.Run(tray.Options{
		IconICO: iconICO,
		Tooltip: func() string { return trim("Macawlink — "+a.Status().Message, 120) },
		OnClick: func() { openURL(url) },
		Notify:  notify,
		Menu:    func() []tray.Item { return menu(a, url, logFile) },
	})
	if err != nil {
		log.Printf("tray: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	log.Printf("bye")
}

func menu(a *app.App, url, logFile string) []tray.Item {
	st := a.Status()
	s := a.Settings()
	check := func(on bool, t string) string {
		if on {
			return "✓ " + t
		}
		return "    " + t
	}
	presets := []tray.Item{}
	labels := map[string]string{"low": "Low bandwidth (480p)", "meeting": "Meeting (720p30)", "max": "Max quality (1080p60)"}
	for _, n := range config.PresetNames {
		n := n
		p := config.Presets[n]
		on := p.Video.Width == s.Video.Width && p.Video.FPS == s.Video.FPS && p.Audio.SampleRate == s.Audio.SampleRate
		presets = append(presets, tray.Item{Title: check(on, labels[n]), OnClick: func() { a.ApplyPreset(n) }})
	}
	lens := func(l, title string) tray.Item {
		return tray.Item{Title: check(s.Video.Lens == l, title), OnClick: func() {
			ns := a.Settings()
			ns.Video.Lens = l
			ns.Video.Mirror = l == "front"
			a.ApplySettings(ns)
		}}
	}
	return []tray.Item{
		{Title: trim("● "+st.Message, 70)},
		{Separator: true},
		{Title: "Open settings…", OnClick: func() { openURL(url) }},
		{Title: "Quality", Sub: presets},
		{Title: "Camera", Sub: []tray.Item{lens("back", "Back camera"), lens("front", "Front camera")}},
		{Title: "Refresh picture", OnClick: a.RequestKeyframe},
		{Title: "Wi-Fi && paired phones…", OnClick: func() { openURL(url) }},
		{Separator: true},
		{Title: "Open log folder", OnClick: func() { openURL(filepath.Dir(logFile)) }},
		{Title: "Quit Macawlink", OnClick: tray.Quit},
	}
}

// setupCamera makes sure the camera is registered (offering the one-time admin setup).
// Windows 11 uses the Media Foundation virtual camera; Windows 10 the DirectShow filter.
func setupCamera(exeDir string) app.CameraStatus {
	mf := native.VirtualCameraSupported()
	cam := app.CameraStatus{Supported: true, Mode: "DirectShow"}
	if mf {
		cam.Mode = "Media Foundation"
	}
	ready := func() bool {
		return install.IsInstalledHere(exeDir) && (mf || native.CameraMode() == native.CameraDirectShow)
	}
	if !ready() {
		ans := install.MessageBox("Macawlink setup",
			"Macawlink needs a one-time setup to add “Macawlink Camera” to Windows.\n\n"+
				"It installs to Program Files and asks for administrator rights. Continue?",
			install.MBYesNo|install.MBIconInfo)
		if ans == install.IDYes {
			if err := install.Elevate("--install"); err != nil {
				install.MessageBox("Macawlink setup", err.Error(), install.MBIconError)
			} else if !strings.EqualFold(filepath.Clean(exeDir), filepath.Clean(install.Dir())) {
				// Hand over to the installed copy.
				releaseInstance()
				cmd := exec.Command(filepath.Join(install.Dir(), "macawlink.exe"), os.Args[1:]...)
				if cmd.Start() == nil {
					os.Exit(0)
				}
			}
		}
	}
	if !ready() {
		cam.Error = "Camera not set up yet — run macawlink.exe --install (or accept the setup prompt)."
		return cam
	}
	cam.Registered = true
	if mf {
		onWorker(func() {
			if err := native.StartVirtualCamera(vcam.FriendlyName); err != nil {
				cam.Registered = false
				cam.Error = "Could not start Macawlink Camera: " + err.Error()
			}
		})
	}
	log.Printf("camera: mode=%s registered=%v %s", cam.Mode, cam.Registered, cam.Error)
	return cam
}

// onWorker runs f on a separate OS thread (COM MTA work stays off the tray's UI thread).
func onWorker(f func()) {
	ch := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		f()
		close(ch)
	}()
	<-ch
}

func watchPhase(ctx context.Context, a *app.App, notify chan<- [2]string) {
	last := ""
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := a.Status()
		if st.Phase != last {
			if st.Phase == app.PhaseStreaming {
				notify <- [2]string{"Macawlink connected", st.Message}
			} else if last == app.PhaseStreaming {
				notify <- [2]string{"Macawlink", st.Message}
			}
			last = st.Phase
		}
	}
}

var instanceMutex uintptr

func singleInstance() bool {
	name, _ := syscall.UTF16PtrFromString(`Local\Macawlink.App`)
	h, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(name)))
	instanceMutex = h
	return !(err == syscall.ERROR_ALREADY_EXISTS)
}

func releaseInstance() {
	if instanceMutex != 0 {
		syscall.CloseHandle(syscall.Handle(instanceMutex))
		instanceMutex = 0
	}
}

func openURL(u string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Start()
}

func setupLog(installer bool) string {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Macawlink")
	name := "macawlink.log"
	if installer {
		dir, name = install.DataDir(), "install.log"
	}
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err == nil && st.Size() > 5<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return p
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
