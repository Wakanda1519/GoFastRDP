// Package app wires the host store to the remote desktop launcher.
package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"gofastrdp/internal/rdp"
	"gofastrdp/internal/secret"
	"gofastrdp/internal/store"
)

// App is the backend exposed to the WebView frontend.
type App struct {
	store *store.Store
}

// Info is a small status payload shown in the UI.
type Info struct {
	StorePath    string `json:"storePath"`
	HostCount    int    `json:"hostCount"`
	ProfileCount int    `json:"profileCount"`
}

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "GoFastRDP"), nil
}

// New loads the store and cleans up stale session files.
func New() (*App, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	s, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	rdp.CleanSessions()
	return &App{store: s}, nil
}

// List returns every saved host group with its profiles.
func (a *App) List() []store.HostItem { return a.store.List() }

// SaveHost creates or updates a host group.
func (a *App) SaveHost(in store.SaveHostInput) (store.HostItem, error) {
	return a.store.SaveHost(in)
}

// DeleteHost removes a host group and all of its profiles.
func (a *App) DeleteHost(id string) error { return a.store.DeleteHost(id) }

// SaveProfile creates or updates a profile inside a host group.
func (a *App) SaveProfile(in store.SaveProfileInput) (store.ProfileItem, error) {
	return a.store.SaveProfile(in, secret.Protect)
}

// DeleteProfile removes a profile from its host group.
func (a *App) DeleteProfile(hostID, profileID string) error {
	return a.store.DeleteProfile(hostID, profileID)
}

// ToggleFavorite flips the favorite flag of a profile.
func (a *App) ToggleFavorite(hostID, profileID string) (bool, error) {
	return a.store.ToggleFavorite(hostID, profileID)
}

// ReorderProfiles stores the profile order for a host group.
func (a *App) ReorderProfiles(hostID string, ids []string) error {
	return a.store.ReorderProfiles(hostID, ids)
}

// Info returns build and storage information.
func (a *App) Info() Info {
	hosts := a.store.List()
	profiles := 0
	for _, h := range hosts {
		profiles += len(h.Profiles)
	}
	return Info{
		StorePath:    a.store.Path(),
		HostCount:    len(hosts),
		ProfileCount: profiles,
	}
}

// OpenDataFolder reveals the connection file in Windows Explorer.
func (a *App) OpenDataFolder() error {
	path := a.store.Path()
	args := []string{filepath.Dir(path)}
	if _, err := os.Stat(path); err == nil {
		args = []string{"/select,", path}
	}
	if err := exec.Command("explorer.exe", args...).Start(); err != nil {
		return errors.New("не удалось открыть проводник: " + err.Error())
	}
	return nil
}

// Connect opens a remote desktop session for a profile inside a host group.
func (a *App) Connect(hostID, profileID string) (string, error) {
	host, profile, ok := a.store.Session(hostID, profileID)
	if !ok {
		return "", errors.New("профиль не найден")
	}

	password := ""
	if profile.PasswordEnc != "" {
		var err error
		password, err = secret.Unprotect(profile.PasswordEnc)
		if err != nil {
			return "", errors.New("не удалось расшифровать сохраненный пароль: " +
				"он привязан к вашей учетной записи Windows и не переносится на другой компьютер. " +
				"Введите пароль в профиле заново")
		}
	}

	path, err := rdp.Launch(rdp.Config{
		ID:         profile.ID,
		Host:       host.Host,
		Port:       host.Port,
		Username:   profile.Username,
		Domain:     profile.Domain,
		Password:   password,
		Width:      host.Width,
		Height:     host.Height,
		Fullscreen: host.Fullscreen,
		MultiMon:   host.MultiMon,
		Clipboard:  host.Clipboard,
		AudioMode:  host.AudioMode,
	})
	if err != nil {
		return "", err
	}

	a.store.Touch(hostID, profileID)
	return path, nil
}
