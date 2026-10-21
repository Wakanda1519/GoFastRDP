// Splash window: a small native window drawn with GDI. It appears instantly on
// startup (before WebView2 initializes) and shows an animated spinner, so the
// user never sees an empty frame while the main window loads.
package winui

import (
	"errors"
	"math"
	"syscall"
	"unsafe"
)

const (
	wsPopup        = 0x80000000
	wsBorder       = 0x00800000
	wsExToolWindow = 0x00000080
	wsExTopmost    = 0x00000008

	wmPaint      = 0x000F
	wmTimer      = 0x0113
	wmEraseBkgnd = 0x0014

	splashWidth  = 460
	splashHeight = 232
	spinnerDots  = 8
)

const (
	colorBg         = 0x002A170F // #0F172A
	colorBrand      = 0x00E64A1A // #1A4AE6
	colorBrandLight = 0x00FF662F // #2F66FF
	colorSlate700   = 0x00554133 // #334155
	colorSlate400   = 0x00B8A394 // #94A3B8
	colorWhite      = 0x00FFFFFF
)

var (
	procBeginPaint   = user32.NewProc("BeginPaint")
	procEndPaint     = user32.NewProc("EndPaint")
	procFillRect     = user32.NewProc("FillRect")
	procSetTimer     = user32.NewProc("SetTimer")
	procKillTimer    = user32.NewProc("KillTimer")
	procInvalidate   = user32.NewProc("InvalidateRect")
	procTextOut      = gdi32.NewProc("TextOutW")
	procGetStockObj  = gdi32.NewProc("GetStockObject")
	procSetTextColor = gdi32.NewProc("SetTextColor")
	procSetBkMode    = gdi32.NewProc("SetBkMode")
	procRoundRect    = gdi32.NewProc("RoundRect")
	procEllipse      = gdi32.NewProc("Ellipse")
	procCreatePen    = gdi32.NewProc("CreatePen")
	procCreateFont   = gdi32.NewProc("CreateFontW")
	procSelectObject = gdi32.NewProc("SelectObject")
	procDeleteObject = gdi32.NewProc("DeleteObject")

	splashClassPtr, _ = syscall.UTF16PtrFromString("GoFastRDPSplash")
	splashTitlePtr, _ = syscall.UTF16PtrFromString("GoFastRDP")
	facePtr, _        = syscall.UTF16PtrFromString("Segoe UI")
	splashProcPtr     = syscall.NewCallback(splashProc)

	splashBrush uintptr
	splashSpin  int
	splashDPI   int
	splashAlive bool
)

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     rect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

// Splash is the startup window with the loading spinner.
type Splash struct {
	hwnd uintptr
}

// NewSplash creates and immediately paints the splash window.
func NewSplash() (*Splash, error) {
	enableDPIAwareness()
	splashDPI = systemDPI()

	hInstance, _, _ := procGetModuleHandle.Call(0)
	splashBrush, _, _ = procCreateSolidBrush.Call(colorBg)

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   splashProcPtr,
		hInstance:     hInstance,
		hbrBackground: splashBrush,
		lpszClassName: splashClassPtr,
	}
	if atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return nil, errors.New("RegisterClassEx(splash): " + err.Error())
	}

	w := scale(splashWidth, splashDPI)
	h := scale(splashHeight, splashDPI)
	x, y := centeredOrigin(w, h)

	hwnd, _, err := procCreateWindowEx.Call(
		wsExToolWindow|wsExTopmost,
		uintptr(unsafe.Pointer(splashClassPtr)),
		uintptr(unsafe.Pointer(splashTitlePtr)),
		uintptr(wsPopup|wsBorder),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return nil, errors.New("CreateWindowEx(splash): " + err.Error())
	}

	splashAlive = true
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	procSetForeground.Call(hwnd)
	procSetTimer.Call(hwnd, 1, 70, 0)

	return &Splash{hwnd: hwnd}, nil
}

// Close removes the splash window. Safe to call more than once.
func (s *Splash) Close() {
	if s == nil || s.hwnd == 0 || !splashAlive {
		return
	}
	splashAlive = false
	procKillTimer.Call(s.hwnd, 1)
	procDestroyWindow.Call(s.hwnd)
	s.hwnd = 0
}

func splashProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmPaint:
		paintSplash(hwnd)
		return 0
	case wmTimer:
		splashSpin = (splashSpin + 1) % spinnerDots
		procInvalidate.Call(hwnd, 0, 1)
		return 0
	case wmEraseBkgnd:
		return 1
	case wmDestroy:
		procKillTimer.Call(hwnd, 1)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

func paintSplash(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	bg, _, _ := procCreateSolidBrush.Call(colorBg)
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bg)
	procDeleteObject.Call(bg)

	// Logo tile with a rounded monitor glyph, mirroring the application icon.
	tileX, tileY := spl(36), spl(52)
	tile := spl(56)
	roundedRect(hdc, tileX, tileY, tileX+tile, tileY+tile, spl(12), colorBrand)

	pen, _, _ := procCreatePen.Call(0, uintptr(spl(3)), colorWhite) // PS_SOLID
	oldPen, _, _ := procSelectObject.Call(hdc, pen)
	nullBrush, _, _ := procGetStockObj.Call(5) // NULL_BRUSH
	oldBrush, _, _ := procSelectObject.Call(hdc, nullBrush)

	// Rounded screen outline.
	mx, my := tileX+spl(13), tileY+spl(14)
	procRoundRect.Call(hdc,
		uintptr(mx), uintptr(my),
		uintptr(mx+spl(30)), uintptr(my+spl(21)),
		uintptr(spl(10)), uintptr(spl(10)),
	)

	procSelectObject.Call(hdc, oldBrush)
	procSelectObject.Call(hdc, oldPen)
	procDeleteObject.Call(pen)

	// Rounded stem and base bar.
	roundedRect(hdc, tileX+spl(26), tileY+spl(34), tileX+spl(30), tileY+spl(40), spl(1), colorWhite)
	roundedRect(hdc, tileX+spl(21), tileY+spl(39), tileX+spl(35), tileY+spl(43), spl(2), colorWhite)

	textOut(hdc, "GoFastRDP", tileX+tile+spl(18), tileY+spl(2), spl(21), 600, colorWhite)
	textOut(hdc, "Удаленные рабочие столы", tileX+tile+spl(18), tileY+spl(31), spl(12), 400, colorSlate400)

	// Comet-style spinner: the leading dot is brand blue, the tail fades out.
	cx, cy := spl(64), spl(172)
	radius := float64(spl(17))
	dotRadius := float64(spl(4))
	for i := 0; i < spinnerDots; i++ {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/spinnerDots
		x := float64(cx) + radius*math.Cos(angle)
		y := float64(cy) + radius*math.Sin(angle)
		behind := (splashSpin - i + spinnerDots) % spinnerDots
		c := blendColor(colorBrandLight, colorSlate700, float64(behind)/float64(spinnerDots))
		dot, _, _ := procCreateSolidBrush.Call(c)
		old, _, _ := procSelectObject.Call(hdc, dot)
		procEllipse.Call(hdc,
			uintptr(int32(x-dotRadius)), uintptr(int32(y-dotRadius)),
			uintptr(int32(x+dotRadius)), uintptr(int32(y+dotRadius)),
		)
		procSelectObject.Call(hdc, old)
		procDeleteObject.Call(dot)
	}

	textOut(hdc, "Загрузка подключений…", cx+spl(32), cy-spl(9), spl(13), 400, colorSlate400)
}

func spl(v int) int32 { return scale(v, splashDPI) }

func roundedRect(hdc uintptr, x1, y1, x2, y2, radius int32, color uintptr) {
	b, _, _ := procCreateSolidBrush.Call(color)
	p, _, _ := procCreatePen.Call(0, 1, color)
	oldB, _, _ := procSelectObject.Call(hdc, b)
	oldP, _, _ := procSelectObject.Call(hdc, p)
	procRoundRect.Call(hdc, uintptr(x1), uintptr(y1), uintptr(x2), uintptr(y2),
		uintptr(radius*2), uintptr(radius*2))
	procSelectObject.Call(hdc, oldB)
	procSelectObject.Call(hdc, oldP)
	procDeleteObject.Call(b)
	procDeleteObject.Call(p)
}

func textOut(hdc uintptr, value string, x, y, height, weight int32, color uintptr) {
	font, _, _ := procCreateFont.Call(
		uintptr(-height), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(facePtr)),
	)
	old, _, _ := procSelectObject.Call(hdc, font)
	procSetTextColor.Call(hdc, color)
	procSetBkMode.Call(hdc, 1) // TRANSPARENT
	ptr, _ := syscall.UTF16PtrFromString(value)
	length := len(syscall.StringToUTF16(value)) - 1
	procTextOut.Call(hdc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(ptr)), uintptr(length))
	procSelectObject.Call(hdc, old)
	procDeleteObject.Call(font)
}

func blendColor(from, to uintptr, t float64) uintptr {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	mix := func(shift uint) uintptr {
		a := float64((from >> shift) & 0xFF)
		b := float64((to >> shift) & 0xFF)
		return uintptr(a+(b-a)*t) & 0xFF
	}
	return mix(0) | mix(8)<<8 | mix(16)<<16
}
