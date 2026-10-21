// Package winui creates the native application window that hosts the WebView.
// The window is created hidden so the user never sees an empty white frame
// while WebView2 is still initializing; it is revealed once the UI is ready.
package winui

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsClipChildren     = 0x02000000

	wmDestroy = 0x0002
	wmSize    = 0x0005
	wmClose   = 0x0010
	wmSetIcon = 0x0080

	swShow = 5

	spiGetWorkArea = 0x0030

	imageIcon = 1
	lrShared  = 0x00008000

	smCXIcon   = 11
	smCYIcon   = 12
	smCXSmIcon = 49
	smCYSmIcon = 50

	iconSmall = 0
	iconBig   = 1

	idcArrow       = 32512
	idiApplication = 32512
	appIconID      = 1

	dpiAwarenessPerMonitorV2 = ^uintptr(3) // -4
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procRegisterClassEx    = user32.NewProc("RegisterClassExW")
	procCreateWindowEx     = user32.NewProc("CreateWindowExW")
	procDefWindowProc      = user32.NewProc("DefWindowProcW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procShowWindow         = user32.NewProc("ShowWindow")
	procUpdateWindow       = user32.NewProc("UpdateWindow")
	procSetForeground      = user32.NewProc("SetForegroundWindow")
	procSetFocus           = user32.NewProc("SetFocus")
	procPostQuitMessage    = user32.NewProc("PostQuitMessage")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procMoveWindow         = user32.NewProc("MoveWindow")
	procFindWindowEx       = user32.NewProc("FindWindowExW")
	procAdjustWindowRect   = user32.NewProc("AdjustWindowRectEx")
	procAdjustRectForDPI   = user32.NewProc("AdjustWindowRectExForDpi")
	procSystemParameters   = user32.NewProc("SystemParametersInfoW")
	procLoadCursor         = user32.NewProc("LoadCursorW")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procSetDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procGetDpiForSystem    = user32.NewProc("GetDpiForSystem")
	procLoadImage          = user32.NewProc("LoadImageW")
	procLoadIcon           = user32.NewProc("LoadIconW")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procSendMessage        = user32.NewProc("SendMessageW")

	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")

	classNamePtr, _   = syscall.UTF16PtrFromString("GoFastRDPWindow")
	widgetClassPtr, _ = syscall.UTF16PtrFromString("webview_widget")
	wndProcCallback   = syscall.NewCallback(wndProc)

	// brush is kept alive for the lifetime of the window class.
	brush uintptr
)

type rect struct {
	left, top, right, bottom int32
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// Window is the top-level native window hosting the WebView widget.
type Window struct {
	hwnd uintptr
}

// Handle returns the raw HWND.
func (w *Window) Handle() uintptr { return w.hwnd }

// New creates a hidden top-level window with the given client size in logical
// pixels (scaled for the system DPI).
func New(title string, width, height int) (*Window, error) {
	enableDPIAwareness()
	initCOM()

	hInstance, _, _ := procGetModuleHandle.Call(0)
	cursor, _, _ := procLoadCursor.Call(0, uintptr(idcArrow))
	background, _, _ := procCreateSolidBrush.Call(uintptr(0x002A170F)) // slate-900
	brush = background
	bigIcon, smallIcon := loadAppIcons(hInstance)

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   wndProcCallback,
		hInstance:     hInstance,
		hIcon:         bigIcon,
		hCursor:       cursor,
		hbrBackground: background,
		lpszClassName: classNamePtr,
		hIconSm:       smallIcon,
	}
	if atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return nil, errors.New("RegisterClassEx: " + err.Error())
	}

	dpi := systemDPI()
	frame := rect{right: scale(width, dpi), bottom: scale(height, dpi)}
	adjustWindowRect(&frame, dpi)
	frameW := frame.right - frame.left
	frameH := frame.bottom - frame.top

	x, y := centeredOrigin(frameW, frameH)

	titlePtr, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, err := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(wsOverlappedWindow|wsClipChildren),
		uintptr(x), uintptr(y),
		uintptr(frameW), uintptr(frameH),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return nil, errors.New("CreateWindowEx: " + err.Error())
	}

	// Class icons cover the frame, but setting them explicitly also gives the
	// taskbar the intended large icon on every Windows version.
	procSendMessage.Call(hwnd, wmSetIcon, iconBig, bigIcon)
	procSendMessage.Call(hwnd, wmSetIcon, iconSmall, smallIcon)

	return &Window{hwnd: hwnd}, nil
}

// loadAppIcons reads the icon embedded in the executable's resources at the
// sizes Windows asks for. A missing resource falls back to the default app icon.
func loadAppIcons(hInstance uintptr) (big, small uintptr) {
	big = loadIconAt(hInstance, smCXIcon, smCYIcon)
	small = loadIconAt(hInstance, smCXSmIcon, smCYSmIcon)
	if big == 0 {
		big, _, _ = procLoadIcon.Call(0, idiApplication)
	}
	if small == 0 {
		small = big
	}
	return big, small
}

func loadIconAt(hInstance uintptr, cxMetric, cyMetric uintptr) uintptr {
	cx, _, _ := procGetSystemMetrics.Call(cxMetric)
	cy, _, _ := procGetSystemMetrics.Call(cyMetric)
	icon, _, _ := procLoadImage.Call(hInstance, appIconID, imageIcon, cx, cy, lrShared)
	return icon
}

// SyncWidget stretches the WebView child widget over the whole client area.
// The engine only auto-resizes the widget when it owns the window, so the
// host window must do it on every size change.
func (w *Window) SyncWidget() { resizeWidget(w.hwnd) }

// Show reveals the fully rendered window and gives it focus.
func (w *Window) Show() {
	procShowWindow.Call(w.hwnd, swShow)
	procUpdateWindow.Call(w.hwnd)
	procSetForeground.Call(w.hwnd)
	procSetFocus.Call(w.hwnd)
}

// Visible reports whether the window is currently shown.
func (w *Window) Visible() bool {
	r, _, _ := procIsWindowVisible.Call(w.hwnd)
	return r != 0
}

// WebView2Available reports whether the Microsoft Edge WebView2 Runtime is
// installed for this machine or the current user.
func WebView2Available() bool {
	roots := []string{
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramFiles"),
		os.Getenv("LOCALAPPDATA"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		pattern := filepath.Join(root, "Microsoft", "EdgeWebView", "Application", "*", "msedgewebview2.exe")
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSize:
		resizeWidget(hwnd)
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

func resizeWidget(hwnd uintptr) {
	child, _, _ := procFindWindowEx.Call(hwnd, 0, uintptr(unsafe.Pointer(widgetClassPtr)), 0)
	if child == 0 {
		return
	}
	var rc rect
	if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); ok == 0 {
		return
	}
	procMoveWindow.Call(child, 0, 0, uintptr(rc.right-rc.left), uintptr(rc.bottom-rc.top), 1)
}

func adjustWindowRect(frame *rect, dpi int) {
	if procAdjustRectForDPI.Find() == nil {
		procAdjustRectForDPI.Call(
			uintptr(unsafe.Pointer(frame)),
			uintptr(wsOverlappedWindow),
			0, 0,
			uintptr(dpi),
		)
		return
	}
	procAdjustWindowRect.Call(
		uintptr(unsafe.Pointer(frame)),
		uintptr(wsOverlappedWindow),
		0, 0,
	)
}

func centeredOrigin(frameW, frameH int32) (int32, int32) {
	var work rect
	ok, _, _ := procSystemParameters.Call(
		spiGetWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0,
	)
	if ok == 0 {
		return 0, 0
	}
	x := work.left + (work.right-work.left-frameW)/2
	y := work.top + (work.bottom-work.top-frameH)/2
	if x < work.left {
		x = work.left
	}
	if y < work.top {
		y = work.top
	}
	return x, y
}

func enableDPIAwareness() {
	if procSetDpiAwarenessCtx.Find() == nil {
		if r, _, _ := procSetDpiAwarenessCtx.Call(dpiAwarenessPerMonitorV2); r != 0 {
			return
		}
	}
	if procSetProcessDPIAware.Find() == nil {
		procSetProcessDPIAware.Call()
	}
}

// initCOM initializes the apartment-threaded COM model. The WebView engine
// does this itself only when it creates its own window, so a host-provided
// window must initialize COM before the WebView2 controller is created.
func initCOM() {
	procCoInitializeEx.Call(0, 0x2) // COINIT_APARTMENTTHREADED
}

func systemDPI() int {
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi > 0 {
			return int(dpi)
		}
	}
	return 96
}

func scale(value, dpi int) int32 {
	return int32(float64(value)*float64(dpi)/96.0 + 0.5)
}
