// GoFastRDP is a single-binary desktop client for managing and opening
// Remote Desktop sessions without copying credentials by hand.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	webview "github.com/webview/webview_go"

	"gofastrdp/internal/app"
	"gofastrdp/internal/store"
	"gofastrdp/internal/winui"
)

//go:embed web/index.html web/app.js web/dist
var assets embed.FS

const (
	windowWidth   = 1240
	windowHeight  = 800
	readyTimeout  = 3 * time.Second
	minSplashTime = 600 * time.Millisecond

	// 'unsafe-eval' is required by the injected WebView bridge, which builds
	// the binding stubs with eval. Inline scripts stay blocked.
	contentSecurityPolicy = "default-src 'none'; script-src 'self' 'unsafe-eval'; " +
		"style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

var errUnauthorized = errors.New("вызов API отклонен")

func main() {
	// Must run before any LoadLibrary call: a DLL planted next to the
	// executable (for example WebView2Loader.dll) must never be loaded.
	winui.SecureDLLSearch()

	releaseInstance, acquired := winui.SingleInstance()
	defer releaseInstance()
	if !acquired {
		return
	}

	if !winui.WebView2Available() {
		fatal("Для работы GoFastRDP требуется Microsoft Edge WebView2 Runtime.\n" +
			"Установите его и запустите приложение снова.")
	}

	// The splash appears instantly and animates while WebView2 starts up.
	splash, splashErr := winui.NewSplash()
	if splashErr != nil {
		log.Println("не удалось показать заставку:", splashErr)
		splash = nil
	}
	defer splash.Close()
	splashShownAt := time.Now()

	backend, err := app.New()
	if err != nil {
		fatal("Не удалось инициализировать хранилище подключений:\n" + err.Error())
	}

	token, err := newAPIToken()
	if err != nil {
		fatal("Не удалось подготовить локальный интерфейс:\n" + err.Error())
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal("Не удалось запустить локальный интерфейс:\n" + err.Error())
	}
	localHost := ln.Addr().String()

	handler, err := newHandler(localHost, token)
	if err != nil {
		fatal(err.Error())
	}
	go func() {
		if err := http.Serve(ln, handler); err != nil {
			log.Println("локальный сервер остановлен:", err)
		}
	}()

	// The window is created hidden: WebView2 needs a moment to initialize and
	// showing it earlier would flash an empty white frame at the user.
	nativeWin, err := winui.New("GoFastRDP", windowWidth, windowHeight)
	if err != nil {
		fatal("Не удалось создать окно приложения:\n" + err.Error())
	}

	// The engine accepts either an HWND value or a pointer to one; the pointer
	// form keeps the handle conversion out of an unsafe.Pointer(uintptr).
	hwnd := nativeWin.Handle()
	w := webview.NewWindow(false, unsafe.Pointer(&hwnd))
	defer w.Destroy()

	w.SetTitle("GoFastRDP")
	nativeWin.SyncWidget()

	var showOnce sync.Once
	show := func() {
		showOnce.Do(func() {
			nativeWin.Show()
			splash.Close()
		})
	}

	var readyRequested atomic.Bool
	// Hand the screen over once the UI is rendered, but keep the splash up long
	// enough for the spinner to be readable instead of flashing.
	reveal := func() {
		if readyRequested.Swap(true) {
			return
		}
		if wait := minSplashTime - time.Since(splashShownAt); wait > 0 {
			time.AfterFunc(wait, func() { w.Dispatch(show) })
			return
		}
		show()
	}

	mustBind(w, "api_ready", func(tok string) bool {
		if !authorized(tok, token) {
			return false
		}
		reveal()
		return true
	})
	bindAll(w, backend, token)

	w.Navigate("http://" + localHost + "/")

	// Safety net in case the page never reports readiness.
	time.AfterFunc(readyTimeout, func() {
		w.Dispatch(show)
	})

	w.Run()
}

func newAPIToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func authorized(candidate, token string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1
}

// newHandler serves the embedded frontend over loopback for the WebView.
// Only known assets are exposed, and every response carries hardening headers.
func newHandler(host, token string) (http.Handler, error) {
	sub, err := fs.Sub(assets, "web")
	if err != nil {
		return nil, err
	}

	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	// The page gets its API token from this meta tag; the token is generated
	// per process and required by every binding.
	meta := []byte(`  <meta name="api-token" content="` + token + `" />` + "\n</head>")
	index = bytes.Replace(index, []byte("</head>"), meta, 1)

	appJS, err := fs.ReadFile(sub, "app.js")
	if err != nil {
		return nil, err
	}
	styles, err := fs.ReadFile(sub, "dist/styles.css")
	if err != nil {
		return nil, err
	}

	files := map[string]struct {
		body        []byte
		contentType string
	}{
		"/":                {index, "text/html; charset=utf-8"},
		"/index.html":      {index, "text/html; charset=utf-8"},
		"/app.js":          {appJS, "text/javascript; charset=utf-8"},
		"/dist/styles.css": {styles, "text/css; charset=utf-8"},
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// Reject requests addressed to another host name, which blocks DNS
		// rebinding attempts against the loopback server.
		if r.Host != host {
			http.NotFound(rw, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			rw.Header().Set("Allow", "GET, HEAD")
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		asset, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", asset.contentType)
		rw.Header().Set("Content-Length", strconv.Itoa(len(asset.body)))
		rw.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		rw.Header().Set("X-Content-Type-Options", "nosniff")
		rw.Header().Set("Referrer-Policy", "no-referrer")
		rw.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		rw.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			rw.WriteHeader(http.StatusOK)
			return
		}
		_, _ = rw.Write(asset.body)
	}), nil
}

// bindAll exposes the backend to the page. Every binding requires the
// per-process token, so a page loaded from anywhere else cannot call them.
func bindAll(w webview.WebView, backend *app.App, token string) {
	mustBind(w, "api_list", func(tok string) ([]store.HostItem, error) {
		if !authorized(tok, token) {
			return nil, errUnauthorized
		}
		return backend.List(), nil
	})
	mustBind(w, "api_save_host", func(tok string, in store.SaveHostInput) (store.HostItem, error) {
		if !authorized(tok, token) {
			return store.HostItem{}, errUnauthorized
		}
		return backend.SaveHost(in)
	})
	mustBind(w, "api_delete_host", func(tok string, id string) error {
		if !authorized(tok, token) {
			return errUnauthorized
		}
		return backend.DeleteHost(id)
	})
	mustBind(w, "api_save_profile", func(tok string, in store.SaveProfileInput) (store.ProfileItem, error) {
		if !authorized(tok, token) {
			return store.ProfileItem{}, errUnauthorized
		}
		return backend.SaveProfile(in)
	})
	mustBind(w, "api_delete_profile", func(tok string, hostID string, profileID string) error {
		if !authorized(tok, token) {
			return errUnauthorized
		}
		return backend.DeleteProfile(hostID, profileID)
	})
	mustBind(w, "api_toggle_favorite", func(tok string, hostID string, profileID string) (bool, error) {
		if !authorized(tok, token) {
			return false, errUnauthorized
		}
		return backend.ToggleFavorite(hostID, profileID)
	})
	mustBind(w, "api_reorder_profiles", func(tok string, hostID string, ids []string) error {
		if !authorized(tok, token) {
			return errUnauthorized
		}
		return backend.ReorderProfiles(hostID, ids)
	})
	mustBind(w, "api_connect", func(tok string, hostID string, profileID string) (string, error) {
		if !authorized(tok, token) {
			return "", errUnauthorized
		}
		return backend.Connect(hostID, profileID)
	})
	mustBind(w, "api_info", func(tok string) (app.Info, error) {
		if !authorized(tok, token) {
			return app.Info{}, errUnauthorized
		}
		return backend.Info(), nil
	})
	mustBind(w, "api_open_data_folder", func(tok string) error {
		if !authorized(tok, token) {
			return errUnauthorized
		}
		return backend.OpenDataFolder()
	})
}

func mustBind(w webview.WebView, name string, fn interface{}) {
	if err := w.Bind(name, fn); err != nil {
		fatal("Не удалось зарегистрировать метод " + name + ":\n" + err.Error())
	}
}

// fatal shows a native message box and exits; used before the WebView exists.
func fatal(message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("GoFastRDP")
	text, _ := syscall.UTF16PtrFromString(message)
	msgBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
	os.Exit(1)
}
