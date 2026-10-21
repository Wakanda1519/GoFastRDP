package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCorruptRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Error("expected empty store")
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Errorf("expected one quarantined file, got %v", matches)
	}
}

func TestInputValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(SaveHostInput{Host: "1.2.3.4\r\nevil"}); err == nil {
		t.Error("control characters must be rejected")
	}
	if _, err := s.SaveHost(SaveHostInput{Host: "1.2.3.4", Port: 99999}); err == nil {
		t.Error("invalid port must be rejected")
	}
	h, err := s.SaveHost(SaveHostInput{Host: "1.2.3.4", Width: 999999, Height: -5})
	if err != nil {
		t.Fatal(err)
	}
	if h.Width != 16384 || h.Height != 1080 {
		t.Errorf("dimensions not clamped: %d x %d", h.Width, h.Height)
	}
	if _, err := s.SaveProfile(SaveProfileInput{HostID: h.ID, Name: "x\ny"}, func(string) (string, error) { return "enc", nil }); err == nil {
		t.Error("control characters must be rejected in profile fields")
	}
}

func TestBackupOnPersist(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(SaveHostInput{Host: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(SaveHostInput{Host: "2.2.2.2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName+".bak")); err != nil {
		t.Errorf("backup not written: %v", err)
	}
}

func TestFutureVersionRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"version":99,"hosts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Error("newer file format must not be overwritten")
	}
}
