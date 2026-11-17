//go:build windows

// Package install performs the one-time, admin-only setup: copy Macawlink into Program Files,
// register the camera DLL in HKLM (Frame Server runs as LOCAL SERVICE and only sees HKLM),
// give Frame Server a log folder, and add a Start menu shortcut.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/jose00735/macawlink/windows/internal/native"
)

const CLSID = "{6E0D1A3B-6F2C-4C1E-9B7A-4D61434157B1}"

var (
	shell32         = syscall.NewLazyDLL("shell32.dll")
	user32          = syscall.NewLazyDLL("user32.dll")
	pIsUserAnAdmin  = shell32.NewProc("IsUserAnAdmin")
	pShellExecuteEx = shell32.NewProc("ShellExecuteExW")
	pMessageBox     = user32.NewProc("MessageBoxW")
)

// Files shipped next to macawlink.exe that get installed.
var payload = []string{"macawlink.exe", native.DLLName, "adb.exe", "AdbWinApi.dll", "AdbWinUsbApi.dll", "macawlink.ico", "NOTICE.txt"}

func IsAdmin() bool {
	r, _, _ := pIsUserAnAdmin.Call()
	return r != 0
}

// Dir is %ProgramFiles%\Macawlink.
func Dir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "Macawlink")
}

func DataDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "Macawlink")
}

// RegisteredDLL returns the DLL path registered for the camera CLSID ("" if none).
func RegisteredDLL() string {
	var k syscall.Handle
	key, _ := syscall.UTF16PtrFromString(`SOFTWARE\Classes\CLSID\` + CLSID + `\InprocServer32`)
	if syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, key, 0, syscall.KEY_READ, &k) != nil {
		return ""
	}
	defer syscall.RegCloseKey(k)
	buf := make([]uint16, 1024)
	n := uint32(len(buf) * 2)
	var typ uint32
	if syscall.RegQueryValueEx(k, nil, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &n) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// IsInstalledHere reports whether the camera DLL registered in HKLM is the one in exeDir.
func IsInstalledHere(exeDir string) bool {
	return strings.EqualFold(filepath.Clean(RegisteredDLL()), filepath.Clean(filepath.Join(exeDir, native.DLLName)))
}

// Elevate re-runs this executable as administrator with args and waits for it.
func Elevate(args string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(args)
	type shellExecuteInfo struct {
		Size       uint32
		Mask       uint32
		Hwnd       uintptr
		Verb       *uint16
		File       *uint16
		Parameters *uint16
		Directory  *uint16
		Show       int32
		InstApp    uintptr
		IDList     uintptr
		Class      *uint16
		KeyClass   uintptr
		HotKey     uint32
		IconOrMon  uintptr
		Process    syscall.Handle
	}
	sei := shellExecuteInfo{Mask: 0x40 /* SEE_MASK_NOCLOSEPROCESS */, Verb: verb, File: file, Parameters: params, Show: 0}
	sei.Size = uint32(unsafe.Sizeof(sei))
	if r, _, e := pShellExecuteEx.Call(uintptr(unsafe.Pointer(&sei))); r == 0 {
		return fmt.Errorf("elevation cancelled or failed: %v", e)
	}
	if sei.Process != 0 {
		syscall.WaitForSingleObject(sei.Process, syscall.INFINITE)
		var code uint32
		syscall.GetExitCodeProcess(sei.Process, &code)
		syscall.CloseHandle(sei.Process)
		if code != 0 {
			return fmt.Errorf("installer exited with code %d (see %s)", code, filepath.Join(DataDir(), "install.log"))
		}
	}
	return nil
}

// Install must run elevated. srcDir holds the unpacked release.
func Install(srcDir string) error {
	if !IsAdmin() {
		return errors.New("administrator rights required")
	}
	dst := Dir()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range payload {
		src := filepath.Join(srcDir, f)
		if _, err := os.Stat(src); err != nil {
			continue // optional files (adb) may be missing
		}
		if strings.EqualFold(filepath.Clean(srcDir), filepath.Clean(dst)) {
			break
		}
		if err := copyFile(src, filepath.Join(dst, f)); err != nil {
			return fmt.Errorf("copy %s: %w", f, err)
		}
	}
	// Log folder writable by Frame Server (LOCAL SERVICE) and users.
	os.MkdirAll(DataDir(), 0o755)
	run("icacls", DataDir(), "/grant", "*S-1-5-19:(OI)(CI)M", "*S-1-5-32-545:(OI)(CI)M")

	// Register the camera media source from its final location.
	if err := native.Load(dst); err != nil {
		return fmt.Errorf("load %s: %w", native.DLLName, err)
	}
	if err := native.Register(); err != nil {
		return fmt.Errorf("register camera: %w", err)
	}
	shortcut(filepath.Join(dst, "macawlink.exe"))
	return nil
}

// Uninstall removes registration and the shortcut; files are removed after we exit.
func Uninstall(exeDir string) error {
	if !IsAdmin() {
		return errors.New("administrator rights required")
	}
	if native.Load(exeDir) == nil {
		native.Unregister()
	}
	os.Remove(shortcutPath())
	if strings.EqualFold(filepath.Clean(exeDir), filepath.Clean(Dir())) {
		cmd := exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >nul & rmdir /s /q \""+Dir()+"\"")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		cmd.Start()
	}
	return nil
}

func shortcutPath() string {
	return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\Macawlink.lnk`)
}

func shortcut(target string) {
	ps := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.IconLocation='%s';$s.Description='Use your phone as webcam and mic';$s.Save()`,
		shortcutPath(), target, filepath.Dir(target), filepath.Join(filepath.Dir(target), "macawlink.ico"))
	run("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// MessageBox flags.
const (
	MBOK          = 0x0
	MBYesNo       = 0x4
	MBIconInfo    = 0x40
	MBIconWarning = 0x30
	MBIconError   = 0x10
	IDYes         = 6
)

func MessageBox(title, text string, flags uint32) int {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := pMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(flags|0x10000 /* MB_SETFOREGROUND */))
	return int(r)
}
