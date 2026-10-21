// Package rdp builds .rdp session files and launches the Windows Remote Desktop client.
package rdp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"gofastrdp/internal/secret"
)

const (
	defaultPort = 3389

	// The session file carries a DPAPI-protected password, so it is deleted
	// shortly after the client had a chance to read it.
	sessionFileTTL = 30 * time.Second
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemDirectory = kernel32.NewProc("GetSystemDirectoryW")

	sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// Config describes a single remote desktop session.
type Config struct {
	ID         string
	Host       string
	Port       int
	Username   string
	Domain     string
	Password   string
	Width      int
	Height     int
	Fullscreen bool
	MultiMon   bool
	Clipboard  bool
	AudioMode  int
}

func sessionDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "GoFastRDP", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// CleanSessions removes session files left behind by previous runs.
func CleanSessions() {
	dir, err := sessionDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".rdp") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Build renders the content of a .rdp file for cfg.
func Build(cfg Config) (string, error) {
	address, err := sessionAddress(cfg.Host, cfg.Port)
	if err != nil {
		return "", err
	}
	username, err := sanitizeField("имя пользователя", cfg.Username, 256)
	if err != nil {
		return "", err
	}
	domain, err := sanitizeField("домен", cfg.Domain, 256)
	if err != nil {
		return "", err
	}

	width, height := cfg.Width, cfg.Height
	if width <= 0 {
		width = 1920
	}
	if height <= 0 {
		height = 1080
	}
	width = clamp(width, 320, 16384)
	height = clamp(height, 200, 16384)

	audioMode := cfg.AudioMode
	if audioMode < 0 || audioMode > 2 {
		audioMode = 0
	}

	var b strings.Builder
	write := func(key string, value string) {
		fmt.Fprintf(&b, "%s:%s\r\n", key, value)
	}
	writeI := func(key string, value int) {
		fmt.Fprintf(&b, "%s:i:%d\r\n", key, value)
	}
	writeS := func(key string, value string) {
		fmt.Fprintf(&b, "%s:s:%s\r\n", key, value)
	}
	writeB := func(key string, value bool) {
		if value {
			writeI(key, 1)
		} else {
			writeI(key, 0)
		}
	}

	if cfg.Fullscreen {
		writeI("screen mode id", 2)
	} else {
		writeI("screen mode id", 1)
	}
	writeI("use multimon", boolInt(cfg.MultiMon))
	writeI("desktopwidth", width)
	writeI("desktopheight", height)
	writeI("session bpp", 32)
	writeS("full address", address)
	writeI("compression", 1)
	writeI("authentication level", 2)
	writeI("enablecredsspsupport", 1)
	writeI("prompt for credentials", 0)
	writeI("promptcredentialonce", 0)
	writeI("audiomode", audioMode)
	writeB("redirectclipboard", cfg.Clipboard)
	writeB("redirectprinters", false)
	writeB("redirectcomports", false)
	writeB("redirectsmartcards", false)
	writeI("keyboardhook", 2)
	writeI("bandwidthautodetect", 1)
	writeI("networkautodetect", 1)
	writeI("connection type", 7)
	if username != "" {
		writeS("username", username)
	}
	if domain != "" {
		writeS("domain", domain)
	}
	if enc, err := encodePassword(cfg.Password); err != nil {
		return "", err
	} else if enc != "" {
		write("password 51:b", enc)
	}

	return b.String(), nil
}

// sessionAddress validates the host and renders the "full address" value.
// Line breaks and other control characters are rejected so they cannot inject
// extra directives into the generated .rdp file.
func sessionAddress(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("не указан адрес узла")
	}
	if port < 0 || port > 65535 {
		return "", errors.New("порт должен быть в диапазоне 1-65535")
	}
	for _, r := range host {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return "", errors.New("адрес узла содержит недопустимые символы")
		}
	}
	host = strings.Trim(host, "[]")
	if net.ParseIP(host) == nil && strings.Contains(host, ":") {
		return "", errors.New("некорректный адрес узла")
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	if port != 0 && port != defaultPort {
		return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port)), nil
	}
	return host, nil
}

// sanitizeField trims a text value used in the .rdp file and rejects control
// characters and oversized values.
func sanitizeField(label, value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > max {
		return "", fmt.Errorf("%s слишком длинное (не более %d символов)", label, max)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s содержит недопустимые символы", label)
		}
	}
	return value, nil
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// encodePassword returns the DPAPI-protected UTF-16LE password as hex, the
// format the Remote Desktop client expects in "password 51:b".
func encodePassword(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	units := utf16.Encode([]rune(password))
	raw := make([]byte, 0, len(units)*2)
	for _, u := range units {
		raw = append(raw, byte(u), byte(u>>8))
	}
	protected, err := secret.ProtectRaw(raw)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(protected)), nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// mstscPath resolves the Remote Desktop client through the system directory
// instead of the SystemRoot environment variable, which the caller controls.
func mstscPath() (string, error) {
	if dir := systemDirectory(); dir != "" {
		p := filepath.Join(dir, "mstsc.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("не удалось найти клиент удаленного рабочего стола (mstsc.exe)")
}

func systemDirectory() string {
	var buf [syscall.MAX_PATH]uint16
	n, _, _ := procGetSystemDirectory.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n == 0 || n >= uintptr(len(buf)) {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// sessionFileName keeps generated files inside the session directory even if
// the stored id was tampered with.
func sessionFileName(id string) string {
	if sessionIDPattern.MatchString(id) {
		return id
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "session"
	}
	return hex.EncodeToString(buf)
}

// Launch writes the session file and starts the Remote Desktop client.
// It returns the path of the generated file.
func Launch(cfg Config) (string, error) {
	content, err := Build(cfg)
	if err != nil {
		return "", err
	}
	dir, err := sessionDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, sessionFileName(cfg.ID)+".rdp")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}

	exe, err := mstscPath()
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	cmd := exec.Command(exe, path)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("не удалось запустить клиент удаленного рабочего стола: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	// The file holds a DPAPI-protected password: drop it as soon as the client
	// has read the settings.
	time.AfterFunc(sessionFileTTL, func() { _ = os.Remove(path) })
	return path, nil
}
