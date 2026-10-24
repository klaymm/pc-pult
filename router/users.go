package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"sync"
	"time"
)

// Permissions

var allPerms = []string{
	"power_on", "status", "power", "screen", "media", "apps",
	"anydesk", "anydesk_admin", "clipboard", "files", "users",
}

var permTitles = map[string]string{
	"power_on":      "Включение",
	"status":        "Состояние",
	"power":         "Питание",
	"screen":        "Скриншоты",
	"media":         "Звук и сообщения",
	"apps":          "Программы",
	"anydesk":       "AnyDesk",
	"anydesk_admin": "AnyDesk: доступ",
	"clipboard":     "Буфер обмена",
	"files":         "Файлы",
	"users":         "Пользователи",
}

var rolePresets = map[string][]string{
	"guest": {"power_on", "status"},
	"user":  {"power_on", "status", "power", "screen", "media", "apps", "anydesk"},
	"admin": {"power_on", "status", "power", "screen", "media", "apps", "anydesk", "anydesk_admin", "clipboard", "files"},
}

var roleOrder = []string{"guest", "user", "admin"}

var roleTitles = map[string]string{
	"owner":  "Владелец",
	"admin":  "Админ",
	"user":   "Пользователь",
	"guest":  "Гость",
	"custom": "Особые права",
}

var roleIcons = map[string]string{
	"owner":  "👑",
	"admin":  "🛡",
	"user":   "👤",
	"guest":  "👋",
	"custom": "⚙️",
}

// Users

type User struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Username string    `json:"username,omitempty"`
	Perms    []string  `json:"perms"`
	Notify   bool      `json:"notify"`
	Added    time.Time `json:"added"`
	Owner    bool      `json:"-"`
}

func (u *User) Has(perm string) bool {
	return u.Owner || slices.Contains(u.Perms, perm)
}

func (u *User) Role() string {
	if u.Owner {
		return "owner"
	}
	for _, r := range roleOrder {
		if sameSet(u.Perms, rolePresets[r]) {
			return r
		}
	}
	return "custom"
}

func (u *User) EffectivePerms() []string {
	if u.Owner {
		return slices.Clone(allPerms)
	}
	return slices.Clone(u.Perms)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

type Invite struct {
	Code    string    `json:"code"`
	Role    string    `json:"role"`
	Expires time.Time `json:"expires"`
	By      int64     `json:"by"`
}

type storeData struct {
	Users   []*User   `json:"users"`
	Invites []*Invite `json:"invites"`
	Ignored []int64   `json:"ignored"`
}

var (
	errNotFound = errors.New("user not found")
	errOwner    = errors.New("owner cannot be changed")
	errExists   = errors.New("user already exists")
	errInvite   = errors.New("invite is invalid or expired")
	errRole     = errors.New("unknown role")
)

type Store struct {
	mu    sync.Mutex
	path  string
	owner int64
	now   func() time.Time
	data  storeData
}

func openStore(path string, owner int64) (*Store, error) {
	s := &Store{path: path, owner: owner, now: time.Now}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, err
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	if s.find(owner) == nil {
		s.data.Users = append(s.data.Users, &User{ID: owner, Name: "Владелец", Perms: slices.Clone(allPerms), Notify: true, Added: s.now()})
	}
	for _, u := range s.data.Users {
		u.Owner = u.ID == owner
		u.Perms = migratePerms(u.Perms)
	}
	return s, s.save()
}

func migratePerms(perms []string) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		if p == "terminal" {
			p = "clipboard"
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, raw, 0o600)
}

func (s *Store) find(id int64) *User {
	for _, u := range s.data.Users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *Store) Get(id int64) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.find(id)
	if u == nil {
		return nil
	}
	c := *u
	c.Perms = slices.Clone(u.Perms)
	return &c
}

func (s *Store) List() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, len(s.data.Users))
	for _, u := range s.data.Users {
		c := *u
		c.Perms = slices.Clone(u.Perms)
		out = append(out, c)
	}
	rank := map[string]int{"owner": 0, "admin": 1, "custom": 2, "user": 3, "guest": 4}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[out[i].Role()], rank[out[j].Role()]
		if ri != rj {
			return ri < rj
		}
		return out[i].Added.Before(out[j].Added)
	})
	return out
}

func (s *Store) Add(id int64, name, username, role string) (*User, error) {
	preset, ok := rolePresets[role]
	if !ok {
		return nil, errRole
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.find(id) != nil {
		return nil, errExists
	}
	if name == "" {
		name = "id " + itoa(id)
	}
	u := &User{ID: id, Name: name, Username: username, Perms: slices.Clone(preset), Notify: true, Added: s.now()}
	s.data.Users = append(s.data.Users, u)
	s.data.Ignored = slices.DeleteFunc(s.data.Ignored, func(x int64) bool { return x == id })
	c := *u
	return &c, s.save()
}

func (s *Store) mutate(id int64, fn func(u *User)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.find(id)
	if u == nil {
		return errNotFound
	}
	if u.Owner {
		return errOwner
	}
	fn(u)
	return s.save()
}

func (s *Store) SetRole(id int64, role string) error {
	preset, ok := rolePresets[role]
	if !ok {
		return errRole
	}
	return s.mutate(id, func(u *User) { u.Perms = slices.Clone(preset) })
}

func (s *Store) TogglePerm(id int64, perm string) error {
	if !slices.Contains(allPerms, perm) {
		return errRole
	}
	return s.mutate(id, func(u *User) {
		if slices.Contains(u.Perms, perm) {
			u.Perms = slices.DeleteFunc(u.Perms, func(p string) bool { return p == perm })
			return
		}
		u.Perms = append(u.Perms, perm)
		sort.SliceStable(u.Perms, func(i, j int) bool {
			return slices.Index(allPerms, u.Perms[i]) < slices.Index(allPerms, u.Perms[j])
		})
	})
}

func (s *Store) ToggleNotify(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.find(id)
	if u == nil {
		return false, errNotFound
	}
	u.Notify = !u.Notify
	return u.Notify, s.save()
}

func (s *Store) Remove(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.find(id)
	if u == nil {
		return errNotFound
	}
	if u.Owner {
		return errOwner
	}
	s.data.Users = slices.DeleteFunc(s.data.Users, func(x *User) bool { return x.ID == id })
	return s.save()
}

func (s *Store) Touch(id int64, name, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.find(id)
	if u == nil || (u.Name == name && u.Username == username) || name == "" {
		return
	}
	u.Name, u.Username = name, username
	_ = s.save()
}

func (s *Store) Recipients(perm string) []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []User
	for _, u := range s.data.Users {
		if u.Notify && u.Has(perm) {
			out = append(out, *u)
		}
	}
	return out
}

// Invites

func (s *Store) CreateInvite(role string, by int64, ttl time.Duration) (string, error) {
	if _, ok := rolePresets[role]; !ok {
		return "", errRole
	}
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneInvites()
	s.data.Invites = append(s.data.Invites, &Invite{Code: code, Role: role, Expires: s.now().Add(ttl), By: by})
	return code, s.save()
}

func (s *Store) pruneInvites() {
	now := s.now()
	s.data.Invites = slices.DeleteFunc(s.data.Invites, func(i *Invite) bool { return now.After(i.Expires) })
}

func (s *Store) Redeem(code string, id int64, name, username string) (*User, Invite, error) {
	s.mu.Lock()
	s.pruneInvites()
	idx := slices.IndexFunc(s.data.Invites, func(i *Invite) bool { return i.Code == code })
	if idx < 0 {
		s.mu.Unlock()
		return nil, Invite{}, errInvite
	}
	inv := *s.data.Invites[idx]
	if s.find(id) != nil {
		s.mu.Unlock()
		return nil, inv, errExists
	}
	s.data.Invites = slices.Delete(s.data.Invites, idx, idx+1)
	s.mu.Unlock()
	u, err := s.Add(id, name, username, inv.Role)
	return u, inv, err
}

// Ignore list

func (s *Store) Ignore(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.data.Ignored, id) {
		s.data.Ignored = append(s.data.Ignored, id)
	}
	return s.save()
}

func (s *Store) IsIgnored(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.data.Ignored, id)
}
