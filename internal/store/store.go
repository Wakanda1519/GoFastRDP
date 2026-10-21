// Package store persists remote desktop host groups and their credential
// profiles in a JSON file.
package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	fileName    = "connections.json"
	fileVersion = 2

	// A hand-edited or damaged file must not make the app allocate forever.
	maxFileSize = 16 << 20

	maxNameLen     = 200
	maxHostLen     = 255
	maxUserLen     = 256
	maxNotesLen    = 4096
	maxPasswordLen = 1024
)

// Profile stores credentials for one user inside a host group. PasswordEnc
// holds a DPAPI-protected secret and is never exposed to the UI.
type Profile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Username    string    `json:"username"`
	Domain      string    `json:"domain"`
	PasswordEnc string    `json:"passwordEnc,omitempty"`
	Favorite    bool      `json:"favorite,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	LastUsed    time.Time `json:"lastUsed,omitempty"`
}

// Host is a group of profiles that share one address and one set of session
// settings.
type Host struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	Notes      string     `json:"notes"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	Fullscreen bool       `json:"fullscreen"`
	MultiMon   bool       `json:"multiMon"`
	Clipboard  bool       `json:"clipboard"`
	AudioMode  int        `json:"audioMode"`
	Profiles   []*Profile `json:"profiles"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsed   time.Time  `json:"lastUsed,omitempty"`
}

// ProfileItem is the safe projection of a profile that is sent to the frontend.
type ProfileItem struct {
	ID          string `json:"id"`
	HostID      string `json:"hostId"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	Domain      string `json:"domain"`
	Favorite    bool   `json:"favorite"`
	HasPassword bool   `json:"hasPassword"`
	CreatedAt   string `json:"createdAt"`
	LastUsed    string `json:"lastUsed"`
}

func (p *Profile) item(hostID string) ProfileItem {
	return ProfileItem{
		ID:          p.ID,
		HostID:      hostID,
		Name:        p.Name,
		Username:    p.Username,
		Domain:      p.Domain,
		Favorite:    p.Favorite,
		HasPassword: p.PasswordEnc != "",
		CreatedAt:   formatTime(p.CreatedAt),
		LastUsed:    formatTime(p.LastUsed),
	}
}

// HostItem is the safe projection of a host group that is sent to the frontend.
type HostItem struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Host       string        `json:"host"`
	Port       int           `json:"port"`
	Notes      string        `json:"notes"`
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	Fullscreen bool          `json:"fullscreen"`
	MultiMon   bool          `json:"multiMon"`
	Clipboard  bool          `json:"clipboard"`
	AudioMode  int           `json:"audioMode"`
	Profiles   []ProfileItem `json:"profiles"`
	CreatedAt  string        `json:"createdAt"`
	LastUsed   string        `json:"lastUsed"`
}

func (h *Host) item() HostItem {
	it := HostItem{
		ID:         h.ID,
		Name:       h.Name,
		Host:       h.Host,
		Port:       h.Port,
		Notes:      h.Notes,
		Width:      h.Width,
		Height:     h.Height,
		Fullscreen: h.Fullscreen,
		MultiMon:   h.MultiMon,
		Clipboard:  h.Clipboard,
		AudioMode:  h.AudioMode,
		Profiles:   make([]ProfileItem, 0, len(h.Profiles)),
		CreatedAt:  formatTime(h.CreatedAt),
		LastUsed:   formatTime(h.LastUsed),
	}
	for _, p := range h.Profiles {
		if p != nil {
			it.Profiles = append(it.Profiles, p.item(h.ID))
		}
	}
	return it
}

// SaveHostInput is the payload sent by the frontend for a host group.
type SaveHostInput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Notes      string `json:"notes"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Fullscreen bool   `json:"fullscreen"`
	MultiMon   bool   `json:"multiMon"`
	Clipboard  bool   `json:"clipboard"`
	AudioMode  int    `json:"audioMode"`
}

// SaveProfileInput is the payload sent by the frontend for a profile. A nil
// Password means "keep the stored secret"; an empty string clears it.
type SaveProfileInput struct {
	HostID   string  `json:"hostId"`
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Username string  `json:"username"`
	Domain   string  `json:"domain"`
	Password *string `json:"password"`
}

// Connection is the legacy flat entry kept only to migrate version 1 files.
type Connection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Username    string    `json:"username"`
	Domain      string    `json:"domain"`
	Group       string    `json:"group"`
	Notes       string    `json:"notes"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Fullscreen  bool      `json:"fullscreen"`
	MultiMon    bool      `json:"multiMon"`
	Clipboard   bool      `json:"clipboard"`
	AudioMode   int       `json:"audioMode"`
	PasswordEnc string    `json:"passwordEnc,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	LastUsed    time.Time `json:"lastUsed,omitempty"`
}

type fileFormat struct {
	Version     int           `json:"version"`
	Hosts       []*Host       `json:"hosts"`
	Connections []*Connection `json:"connections,omitempty"`
}

// Store is a concurrency-safe collection of host groups backed by a JSON file.
type Store struct {
	mu    sync.Mutex
	path  string
	hosts []*Host
}

// Open loads (or creates) the store inside dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, fileName)}
	migrated, err := s.load()
	if err != nil {
		return nil, err
	}
	if migrated {
		s.backupLegacy()
		if err := s.persist(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) backupLegacy() {
	backup := s.path + ".v1.bak"
	if _, err := os.Stat(backup); err == nil {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = os.WriteFile(backup, data, 0o600)
}

// Path returns the absolute location of the backing JSON file.
func (s *Store) Path() string { return s.path }

func (s *Store) load() (bool, error) {
	info, statErr := os.Stat(s.path)
	if errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	}
	if statErr != nil {
		return false, statErr
	}
	if info.Size() > maxFileSize {
		return false, s.quarantineCorrupt()
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return false, err
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, nil
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		// Never block startup because of a damaged file: keep it aside so the
		// user can recover it manually, and continue with an empty list.
		return false, s.quarantineCorrupt()
	}
	if f.Version > fileVersion {
		return false, errors.New("файл подключений создан более новой версией GoFastRDP")
	}
	for _, h := range f.Hosts {
		if h == nil {
			continue
		}
		normalizeHost(h)
		s.hosts = append(s.hosts, h)
	}
	migrated := false
	if len(f.Connections) > 0 {
		s.migrateLegacy(f.Connections)
		migrated = true
	}
	return migrated, nil
}

// quarantineCorrupt renames an unreadable file instead of overwriting it, so
// the previous contents are not lost.
func (s *Store) quarantineCorrupt() error {
	target := s.path + ".corrupt-" + time.Now().UTC().Format("20060102-150405")
	if err := os.Rename(s.path, target); err != nil {
		return errors.New("файл подключений поврежден и не может быть сохранен отдельно: " + err.Error())
	}
	return nil
}

func (s *Store) migrateLegacy(conns []*Connection) {
	byAddress := make(map[string]*Host, len(s.hosts)+len(conns))
	for _, h := range s.hosts {
		key := strings.ToLower(h.Host) + ":" + strconv.Itoa(h.Port)
		if _, exists := byAddress[key]; !exists {
			byAddress[key] = h
		}
	}
	created := make([]*Host, 0, len(conns))
	for _, c := range conns {
		if c == nil {
			continue
		}
		normalizeLegacy(c)
		key := strings.ToLower(c.Host) + ":" + strconv.Itoa(c.Port)
		h, ok := byAddress[key]
		if !ok {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name = c.Host
			}
			h = &Host{
				ID:         newID(),
				Name:       name,
				Host:       c.Host,
				Port:       c.Port,
				Notes:      c.Notes,
				Width:      c.Width,
				Height:     c.Height,
				Fullscreen: c.Fullscreen,
				MultiMon:   c.MultiMon,
				Clipboard:  c.Clipboard,
				AudioMode:  c.AudioMode,
				CreatedAt:  c.CreatedAt,
			}
			if h.CreatedAt.IsZero() {
				h.CreatedAt = time.Now().UTC()
			}
			byAddress[key] = h
			s.hosts = append(s.hosts, h)
			created = append(created, h)
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = strings.TrimSpace(c.Username)
		}
		if name == "" {
			name = "Профиль"
		}
		if profileNameExists(h, name) {
			alternate := strings.TrimSpace(c.Username)
			if alternate != "" && !profileNameExists(h, alternate) {
				name = alternate
			} else {
				base := name
				for i := 2; ; i++ {
					candidate := base + " " + strconv.Itoa(i)
					if !profileNameExists(h, candidate) {
						name = candidate
						break
					}
				}
			}
		}
		h.Profiles = append(h.Profiles, &Profile{
			ID:          newID(),
			Name:        name,
			Username:    c.Username,
			Domain:      c.Domain,
			PasswordEnc: c.PasswordEnc,
			CreatedAt:   c.CreatedAt,
			LastUsed:    c.LastUsed,
		})
	}
	for _, h := range created {
		if len(h.Profiles) > 1 {
			h.Name = h.Host
		}
	}
}

func profileNameExists(h *Host, name string) bool {
	for _, p := range h.Profiles {
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (s *Store) persist() error {
	// Caller holds the lock.
	f := fileFormat{Version: fileVersion, Hosts: s.hosts}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Keep the previous good file as a fallback for manual recovery.
	if _, err := os.Stat(s.path); err == nil {
		_ = copyFile(s.path, s.path+".bak")
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}

// List returns every host group in its stored order.
func (s *Store) List() []HostItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HostItem, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, h.item())
	}
	return out
}

func (s *Store) hostLocked(id string) *Host {
	for _, h := range s.hosts {
		if h.ID == id {
			return h
		}
	}
	return nil
}

// SaveHost creates or updates a host group and returns its projection.
func (s *Store) SaveHost(in SaveHostInput) (HostItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	in.Host = strings.TrimSpace(in.Host)
	if in.Host == "" {
		return HostItem{}, errors.New("укажите адрес узла (IP или имя)")
	}
	host, err := cleanField("адрес узла", in.Host, maxHostLen)
	if err != nil {
		return HostItem{}, err
	}
	if strings.IndexFunc(host, unicode.IsSpace) >= 0 {
		return HostItem{}, errors.New("адрес узла не должен содержать пробелы")
	}
	in.Host = host
	name, err := cleanField("название группы", in.Name, maxNameLen)
	if err != nil {
		return HostItem{}, err
	}
	if name == "" {
		name = in.Host
	}
	in.Name = name
	notes, err := cleanField("заметки", in.Notes, maxNotesLen)
	if err != nil {
		return HostItem{}, err
	}
	in.Notes = notes
	if in.Port == 0 {
		in.Port = 3389
	}
	if in.Port < 1 || in.Port > 65535 {
		return HostItem{}, errors.New("порт должен быть в диапазоне 1-65535")
	}
	in.Width = dimension(in.Width, 1920, 320, 16384)
	in.Height = dimension(in.Height, 1080, 200, 16384)
	if in.AudioMode < 0 || in.AudioMode > 2 {
		in.AudioMode = 0
	}

	var target *Host
	if in.ID != "" {
		target = s.hostLocked(in.ID)
		if target == nil {
			return HostItem{}, errors.New("группа не найдена")
		}
	} else {
		target = &Host{ID: newID(), CreatedAt: time.Now().UTC()}
		s.hosts = append(s.hosts, target)
	}

	target.Name = in.Name
	target.Host = in.Host
	target.Port = in.Port
	target.Notes = in.Notes
	target.Width = in.Width
	target.Height = in.Height
	target.Fullscreen = in.Fullscreen
	target.MultiMon = in.MultiMon
	target.Clipboard = in.Clipboard
	target.AudioMode = in.AudioMode
	normalizeHost(target)

	if err := s.persist(); err != nil {
		return HostItem{}, err
	}
	return target.item(), nil
}

// DeleteHost removes a host group together with all of its profiles.
func (s *Store) DeleteHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, h := range s.hosts {
		if h.ID == id {
			s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
			return s.persist()
		}
	}
	return errors.New("группа не найдена")
}

// SaveProfile creates or updates a profile inside a host group.
func (s *Store) SaveProfile(in SaveProfileInput, protect func(string) (string, error)) (ProfileItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	host := s.hostLocked(in.HostID)
	if host == nil {
		return ProfileItem{}, errors.New("сначала сохраните группу")
	}

	name, err := cleanField("название профиля", in.Name, maxNameLen)
	if err != nil {
		return ProfileItem{}, err
	}
	username, err := cleanField("имя пользователя", in.Username, maxUserLen)
	if err != nil {
		return ProfileItem{}, err
	}
	domain, err := cleanField("домен", in.Domain, maxUserLen)
	if err != nil {
		return ProfileItem{}, err
	}
	in.Name, in.Username, in.Domain = name, username, domain
	if in.Name == "" {
		if in.Username != "" {
			in.Name = in.Username
		} else {
			in.Name = "Профиль"
		}
	}
	if in.Password != nil && len([]rune(*in.Password)) > maxPasswordLen {
		return ProfileItem{}, errors.New("пароль слишком длинный")
	}

	var target *Profile
	if in.ID != "" {
		for _, p := range host.Profiles {
			if p.ID == in.ID {
				target = p
				break
			}
		}
		if target == nil {
			return ProfileItem{}, errors.New("профиль не найден")
		}
	} else {
		target = &Profile{ID: newID(), CreatedAt: time.Now().UTC()}
		host.Profiles = append(host.Profiles, target)
	}

	target.Name = in.Name
	target.Username = in.Username
	target.Domain = in.Domain
	normalizeProfile(target)

	if in.Password != nil {
		if *in.Password == "" {
			target.PasswordEnc = ""
		} else {
			enc, err := protect(*in.Password)
			if err != nil {
				return ProfileItem{}, err
			}
			target.PasswordEnc = enc
		}
	}

	if err := s.persist(); err != nil {
		return ProfileItem{}, err
	}
	return target.item(host.ID), nil
}

// DeleteProfile removes a profile from its host group.
func (s *Store) DeleteProfile(hostID, profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	host := s.hostLocked(hostID)
	if host == nil {
		return errors.New("группа не найдена")
	}
	for i, p := range host.Profiles {
		if p.ID == profileID {
			host.Profiles = append(host.Profiles[:i], host.Profiles[i+1:]...)
			return s.persist()
		}
	}
	return errors.New("профиль не найден")
}

// ToggleFavorite flips the favorite flag and keeps favorite profiles on top.
func (s *Store) ToggleFavorite(hostID, profileID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	host := s.hostLocked(hostID)
	if host == nil {
		return false, errors.New("группа не найдена")
	}
	index := -1
	for i, p := range host.Profiles {
		if p.ID == profileID {
			index = i
			break
		}
	}
	if index < 0 {
		return false, errors.New("профиль не найден")
	}

	profile := host.Profiles[index]
	profile.Favorite = !profile.Favorite
	host.Profiles = append(host.Profiles[:index], host.Profiles[index+1:]...)

	if profile.Favorite {
		host.Profiles = append([]*Profile{profile}, host.Profiles...)
	} else {
		insert := 0
		for insert < len(host.Profiles) && host.Profiles[insert].Favorite {
			insert++
		}
		host.Profiles = append(host.Profiles, nil)
		copy(host.Profiles[insert+1:], host.Profiles[insert:])
		host.Profiles[insert] = profile
	}

	if err := s.persist(); err != nil {
		return false, err
	}
	return profile.Favorite, nil
}

// ReorderProfiles applies the order received from the frontend. Unknown ids are
// ignored and favorites stay ahead of regular profiles.
func (s *Store) ReorderProfiles(hostID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	host := s.hostLocked(hostID)
	if host == nil {
		return errors.New("группа не найдена")
	}

	byID := make(map[string]*Profile, len(host.Profiles))
	for _, p := range host.Profiles {
		byID[p.ID] = p
	}
	seen := make(map[string]bool, len(host.Profiles))
	next := make([]*Profile, 0, len(host.Profiles))
	for _, id := range ids {
		p, ok := byID[id]
		if !ok || seen[id] {
			continue
		}
		next = append(next, p)
		seen[id] = true
	}
	for _, p := range host.Profiles {
		if !seen[p.ID] {
			next = append(next, p)
		}
	}
	sort.SliceStable(next, func(i, j int) bool {
		return next[i].Favorite && !next[j].Favorite
	})
	host.Profiles = next
	return s.persist()
}

// Session returns a copy of a host and one of its profiles.
func (s *Store) Session(hostID, profileID string) (Host, Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range s.hosts {
		if h.ID != hostID {
			continue
		}
		for _, p := range h.Profiles {
			if p.ID == profileID {
				return *h, *p, true
			}
		}
	}
	return Host{}, Profile{}, false
}

// Touch records that a profile was just used.
func (s *Store) Touch(hostID, profileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hosts {
		if h.ID != hostID {
			continue
		}
		for _, p := range h.Profiles {
			if p.ID == profileID {
				now := time.Now().UTC()
				h.LastUsed = now
				p.LastUsed = now
				_ = s.persist()
				return
			}
		}
	}
}

func normalizeHost(h *Host) {
	if h.ID == "" {
		h.ID = newID()
	}
	h.Name = sanitizeStored(h.Name, maxNameLen)
	h.Host = sanitizeStored(h.Host, maxHostLen)
	h.Notes = sanitizeStored(h.Notes, maxNotesLen)
	if h.Port <= 0 || h.Port > 65535 {
		h.Port = 3389
	}
	h.Width = dimension(h.Width, 1920, 320, 16384)
	h.Height = dimension(h.Height, 1080, 200, 16384)
	if h.AudioMode < 0 || h.AudioMode > 2 {
		h.AudioMode = 0
	}
	for _, p := range h.Profiles {
		if p != nil {
			normalizeProfile(p)
		}
	}
}

func normalizeProfile(p *Profile) {
	if p.ID == "" {
		p.ID = newID()
	}
	p.Name = sanitizeStored(p.Name, maxNameLen)
	p.Username = sanitizeStored(p.Username, maxUserLen)
	p.Domain = sanitizeStored(p.Domain, maxUserLen)
	if len(p.PasswordEnc) > 8192 {
		p.PasswordEnc = ""
	}
	if p.Name == "" {
		if p.Username != "" {
			p.Name = p.Username
		} else {
			p.Name = "Профиль"
		}
	}
}

func normalizeLegacy(c *Connection) {
	c.Host = sanitizeStored(c.Host, maxHostLen)
	c.Name = sanitizeStored(c.Name, maxNameLen)
	c.Username = sanitizeStored(c.Username, maxUserLen)
	c.Domain = sanitizeStored(c.Domain, maxUserLen)
	c.Notes = sanitizeStored(c.Notes, maxNotesLen)
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = 3389
	}
	c.Width = dimension(c.Width, 1920, 320, 16384)
	c.Height = dimension(c.Height, 1080, 200, 16384)
}

// cleanField validates a value coming from the frontend.
func cleanField(label, value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > max {
		return "", fmt.Errorf("%s слишком длинное (не более %d символов)", label, max)
	}
	if strings.IndexFunc(value, isControlRune) >= 0 {
		return "", fmt.Errorf("%s содержит недопустимые символы", label)
	}
	return value, nil
}

// sanitizeStored repairs a value read from disk: stale files are fixed up
// instead of blocking startup.
func sanitizeStored(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if isControlRune(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > max {
		value = string(runes[:max])
	}
	return value
}

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// dimension falls back to a default for missing values and clamps the rest.
func dimension(value, fallback, min, max int) int {
	if value <= 0 {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
