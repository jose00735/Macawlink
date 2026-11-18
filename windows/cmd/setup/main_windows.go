//go:build windows

// Command setup is MacawlinkSetup.exe: a single-file installer for the Windows receiver.
//
// It carries the release (macawlink.exe, the camera DLLs, adb, icon, notices) as an embedded
// zip, runs elevated (manifest), and:
//  1. closes a running Macawlink (upgrade),
//  2. extracts into %ProgramFiles%\Macawlink (moving aside files that browsers or Frame
//     Server still have loaded),
//  3. runs `macawlink.exe --install`: camera registration (64- and 32-bit), log folder ACL,
//     Start menu shortcut, firewall rule,
//  4. adds the Settings → Apps entry with Uninstall,
//  5. offers VB-Audio Virtual Cable if no virtual mic is installed, and launches Macawlink.
//
// Flags: /S (or -silent) installs without questions.
package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jose00735/macawlink/windows/internal/app"
	"github.com/jose00735/macawlink/windows/internal/install"
)

//go:embed payload.zip
var payload []byte

const vbCableURL = "https://vb-audio.com/Cable/"

func main() {
	silent := false
	for _, a := range os.Args[1:] {
		switch strings.ToLower(a) {
		case "/s", "-s", "/silent", "-silent", "--silent":
			silent = true
		}
	}
	setupLog()
	title := "Macawlink " + app.Version + " setup"
	ask := func(text string, flags uint32) int {
		if silent {
			return install.IDYes
		}
		return install.MessageBox(title, text, flags)
	}
	fail := func(err error) {
		log.Printf("setup failed: %v", err)
		if !silent {
			install.MessageBox(title, "Setup failed:\n\n"+err.Error()+"\n\nDetails: "+logPath(), install.MBIconError)
		}
		os.Exit(1)
	}

	if !install.IsAdmin() { // the manifest asks for elevation; this covers odd launchers
		fail(fmt.Errorf("please run MacawlinkSetup.exe as administrator"))
	}
	upgrade := fileExists(filepath.Join(install.Dir(), "macawlink.exe"))
	verb := "Install"
	if upgrade {
		verb = "Update"
	}
	if ask(fmt.Sprintf("%s Macawlink %s?\n\nUse your Android phone as a webcam and microphone for this PC, over USB or Wi-Fi.\n\n"+
		"Installs to %s and adds “Macawlink Camera” to Windows.", verb, app.Version, install.Dir()),
		install.MBYesNo|install.MBIconInfo) != install.IDYes {
		os.Exit(0)
	}

	log.Printf("installing %s to %s (upgrade=%v)", app.Version, install.Dir(), upgrade)
	install.StopRunning()
	time.Sleep(500 * time.Millisecond)
	cleanupOld(install.Dir())
	if err := extract(payload, install.Dir()); err != nil {
		fail(err)
	}

	exe := filepath.Join(install.Dir(), "macawlink.exe")
	cmd := exec.Command(exe, "--install")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		fail(fmt.Errorf("camera registration failed (%v); see %s", err, filepath.Join(install.DataDir(), "install.log")))
	}
	install.RegisterUninstall(app.Version)
	log.Printf("installed")

	if silent {
		return
	}
	if !hasVirtualMic() {
		if install.MessageBox(title,
			"One more step for the microphone: Macawlink plays your phone's mic into the free “VB-Audio Virtual Cable”, "+
				"which isn't installed yet.\n\nOpen its download page now? (Install it, then pick “CABLE Output” as the mic in your call apps.)",
			install.MBYesNo|install.MBIconInfo) == install.IDYes {
			openAsUser(vbCableURL)
		}
	}
	if install.MessageBox(title,
		"Macawlink "+app.Version+" is installed.\n\n"+
			"• Phone: install the Macawlink app, then plug in by USB (USB debugging on) or pick this PC under Wi-Fi.\n"+
			"• Call apps: choose “Macawlink Camera”.\n\nStart Macawlink now?",
		install.MBYesNo|install.MBIconInfo) == install.IDYes {
		openAsUser(exe)
	}
}

// hasVirtualMic looks for VB-Cable's (or a future Macawlink) playback endpoint.
func hasVirtualMic() bool {
	out, _ := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\Render`,
		"/s", "/f", "CABLE Input", "/d").CombinedOutput()
	s := string(out)
	return strings.Contains(s, "CABLE Input") || strings.Contains(s, "Macawlink Microphone")
}

// openAsUser starts a program or URL through Explorer so it runs with the signed-in user's
// normal rights, not the installer's elevated ones.
func openAsUser(target string) {
	exec.Command(filepath.Join(os.Getenv("WINDIR"), "explorer.exe"), target).Start()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func logPath() string { return filepath.Join(install.DataDir(), "setup.log") }

func setupLog() {
	os.MkdirAll(install.DataDir(), 0o755)
	if f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags)
}
