//go:build windows

// Package tray shows the Macawlink icon in the notification area (pure Win32 via syscall).
package tray

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassEx       = user32.NewProc("RegisterClassExW")
	pCreateWindowEx        = user32.NewProc("CreateWindowExW")
	pDefWindowProc         = user32.NewProc("DefWindowProcW")
	pGetMessage            = user32.NewProc("GetMessageW")
	pTranslateMessage      = user32.NewProc("TranslateMessage")
	pDispatchMessage       = user32.NewProc("DispatchMessageW")
	pPostMessage           = user32.NewProc("PostMessageW")
	pPostQuitMessage       = user32.NewProc("PostQuitMessage")
	pLoadImage             = user32.NewProc("LoadImageW")
	pCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	pAppendMenu            = user32.NewProc("AppendMenuW")
	pTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	pDestroyMenu           = user32.NewProc("DestroyMenu")
	pGetCursorPos          = user32.NewProc("GetCursorPos")
	pSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	pSetTimer              = user32.NewProc("SetTimer")
	pRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")
	pDestroyWindow         = user32.NewProc("DestroyWindow")
	pShellNotifyIcon       = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandle       = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmCommand   = 0x0111
	wmTimer     = 0x0113
	wmApp       = 0x8000
	wmTray      = wmApp + 1
	wmQuit      = wmApp + 2
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4
	nifInfo    = 0x10

	mfString       = 0x0
	mfGrayed       = 0x1
	mfSeparator    = 0x800
	mfPopup        = 0x10
	tpmReturnCmd   = 0x100
	tpmRightButton = 0x2

	imageIcon      = 1
	lrLoadFromFile = 0x10
	lrDefaultSize  = 0x40
)

type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSm                             uintptr
}

type notifyIconData struct {
	Size             uint32
	Wnd              uintptr
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State, StateMask uint32
	Info             [256]uint16
	Timeout          uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         [16]byte
	BalloonIcon      uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// Item is a menu entry. A nil OnClick makes it a disabled label; Sub makes a submenu.
type Item struct {
	Title     string
	OnClick   func()
	Separator bool
	Sub       []Item
}

type Options struct {
	IconICO []byte           // .ico file contents
	Tooltip func() string    // refreshed every second
	Menu    func() []Item    // rebuilt on every right-click
	OnClick func()           // left click
	Notify  <-chan [2]string // balloon (title, text)
}

var (
	hwnd       uintptr
	nid        notifyIconData
	opts       Options
	actions    []func()
	taskbarMsg uintptr
)

// Run creates the icon and pumps messages until Quit is called. Must own its OS thread.
func Run(o Options) error {
	runtime.LockOSThread()
	opts = o
	inst, _, _ := pGetModuleHandle.Call(0)
	cls, _ := syscall.UTF16PtrFromString("MacawlinkTray")
	wc := wndClassEx{WndProc: syscall.NewCallback(wndProc), Instance: inst, ClassName: cls}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := syscall.UTF16PtrFromString("Macawlink")
	h, _, err := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if h == 0 {
		return err
	}
	hwnd = h
	tbm, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	taskbarMsg, _, _ = pRegisterWindowMessage.Call(uintptr(unsafe.Pointer(tbm)))

	nid = notifyIconData{Wnd: hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmTray}
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.Icon = loadIcon(o.IconICO)
	setTip("Macawlink")
	pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	pSetTimer.Call(hwnd, 1, 1000, 0)

	if o.Notify != nil {
		go func() {
			for n := range o.Notify {
				balloon(n[0], n[1])
			}
		}()
	}

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	return nil
}

// Quit removes the icon and ends Run (safe from any goroutine).
func Quit() {
	if hwnd != 0 {
		pPostMessage.Call(hwnd, wmQuit, 0, 0)
	}
}

func loadIcon(ico []byte) uintptr {
	if len(ico) == 0 {
		return 0
	}
	p := filepath.Join(os.TempDir(), "macawlink-tray.ico")
	if err := os.WriteFile(p, ico, 0o644); err != nil {
		return 0
	}
	u, _ := syscall.UTF16PtrFromString(p)
	h, _, _ := pLoadImage.Call(0, uintptr(unsafe.Pointer(u)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
	return h
}

func setTip(s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(nid.Tip) {
		u = u[:len(nid.Tip)-1]
		u = append(u, 0)
	}
	for i := range nid.Tip {
		nid.Tip[i] = 0
	}
	copy(nid.Tip[:], u)
}

func balloon(title, text string) {
	n := nid
	n.Flags = nifInfo
	t, _ := syscall.UTF16FromString(title)
	b, _ := syscall.UTF16FromString(text)
	copy(n.InfoTitle[:len(n.InfoTitle)-1], t)
	copy(n.Info[:len(n.Info)-1], b)
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

func showMenu() {
	if opts.Menu == nil {
		return
	}
	actions = actions[:0]
	menu := buildMenu(opts.Menu())
	var pt struct{ X, Y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	pDestroyMenu.Call(menu)
	if i := int(cmd) - 1; i >= 0 && i < len(actions) && actions[i] != nil {
		go actions[i]()
	}
}

func buildMenu(items []Item) uintptr {
	m, _, _ := pCreatePopupMenu.Call()
	for _, it := range items {
		switch {
		case it.Separator:
			pAppendMenu.Call(m, mfSeparator, 0, 0)
		case len(it.Sub) > 0:
			sub := buildMenu(it.Sub)
			t, _ := syscall.UTF16PtrFromString(it.Title)
			pAppendMenu.Call(m, mfPopup, sub, uintptr(unsafe.Pointer(t)))
		default:
			actions = append(actions, it.OnClick)
			flags := uintptr(mfString)
			if it.OnClick == nil {
				flags |= mfGrayed
			}
			t, _ := syscall.UTF16PtrFromString(it.Title)
			pAppendMenu.Call(m, flags, uintptr(len(actions)), uintptr(unsafe.Pointer(t)))
		}
	}
	return m
}

func wndProc(h, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmTray:
		switch lParam {
		case wmLButtonUp:
			if opts.OnClick != nil {
				go opts.OnClick()
			}
		case wmRButtonUp:
			showMenu()
		}
		return 0
	case wmTimer:
		if opts.Tooltip != nil {
			setTip(opts.Tooltip())
			pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
		}
		return 0
	case wmQuit, wmClose:
		pDestroyWindow.Call(h)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	if taskbarMsg != 0 && message == taskbarMsg { // Explorer restarted: re-add the icon
		pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, message, wParam, lParam)
	return r
}
