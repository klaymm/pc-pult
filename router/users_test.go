package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "users.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOwnerAlwaysPresent(t *testing.T) {
	s := newTestStore(t)
	o := s.Get(1)
	if o == nil || !o.Owner || o.Role() != "owner" {
		t.Fatalf("owner missing or wrong: %+v", o)
	}
	for _, p := range allPerms {
		if !o.Has(p) {
			t.Errorf("owner lacks %s", p)
		}
	}
	if err := s.Remove(1); !errors.Is(err, errOwner) {
		t.Errorf("remove owner: %v", err)
	}
	if err := s.SetRole(1, "guest"); !errors.Is(err, errOwner) {
		t.Errorf("demote owner: %v", err)
	}
}

func TestRolesAndPerms(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Add(2, "Ann", "ann", "guest"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(2, "Ann", "ann", "guest"); !errors.Is(err, errExists) {
		t.Errorf("duplicate add: %v", err)
	}
	u := s.Get(2)
	if u.Role() != "guest" || u.Has("clipboard") || !u.Has("power_on") {
		t.Fatalf("guest wrong: %+v", u)
	}
	if err := s.TogglePerm(2, "screen"); err != nil {
		t.Fatal(err)
	}
	if u = s.Get(2); u.Role() != "custom" || !u.Has("screen") {
		t.Fatalf("custom wrong: %+v", u)
	}
	if err := s.TogglePerm(2, "screen"); err != nil {
		t.Fatal(err)
	}
	if u = s.Get(2); u.Role() != "guest" {
		t.Fatalf("expected guest again, got %s", u.Role())
	}
	if err := s.SetRole(2, "admin"); err != nil {
		t.Fatal(err)
	}
	if u = s.Get(2); u.Role() != "admin" || u.Has("users") || !u.Has("clipboard") {
		t.Fatalf("admin wrong: %+v", u)
	}
	if err := s.SetRole(2, "boss"); !errors.Is(err, errRole) {
		t.Errorf("unknown role: %v", err)
	}
}

func TestStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	s, err := openStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(5, "Bob", "", "user"); err != nil {
		t.Fatal(err)
	}
	s2, err := openStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u := s2.Get(5); u == nil || u.Role() != "user" {
		t.Fatalf("not persisted: %+v", u)
	}
}

func TestInvites(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	code, err := s.CreateInvite("user", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, inv, err := s.Redeem(code, 7, "Kate", "kate")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role() != "user" || inv.By != 1 {
		t.Fatalf("redeem wrong: %+v %+v", u, inv)
	}
	if _, _, err := s.Redeem(code, 8, "X", ""); !errors.Is(err, errInvite) {
		t.Errorf("second use: %v", err)
	}
	code2, _ := s.CreateInvite("guest", 1, time.Hour)
	now = now.Add(2 * time.Hour)
	if _, _, err := s.Redeem(code2, 9, "Y", ""); !errors.Is(err, errInvite) {
		t.Errorf("expired: %v", err)
	}
}

func TestRecipients(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(2, "A", "", "guest")
	_, _ = s.Add(3, "B", "", "guest")
	if _, err := s.ToggleNotify(3); err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, u := range s.Recipients("status") {
		got[u.ID] = true
	}
	if !got[1] || !got[2] || got[3] {
		t.Fatalf("recipients: %v", got)
	}
	if len(s.Recipients("clipboard")) != 1 {
		t.Fatalf("only owner has clipboard")
	}
}

func TestOldTerminalPermBecomesClipboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	old := `{"users":[{"id":1,"name":"O","perms":["status","terminal","files"],"notify":true},` +
		`{"id":2,"name":"A","perms":["status","terminal","clipboard"],"notify":true}],"invites":[],"ignored":[]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		u := s.Get(id)
		if slices.Contains(u.Perms, "terminal") || !slices.Contains(u.Perms, "clipboard") {
			t.Fatalf("user %d perms: %v", id, u.Perms)
		}
	}
	if got := s.Get(2).Perms; len(got) != 2 {
		t.Fatalf("duplicate not merged: %v", got)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "terminal") {
		t.Fatal("old name still stored")
	}
}

func TestIgnore(t *testing.T) {
	s := newTestStore(t)
	if err := s.Ignore(42); err != nil {
		t.Fatal(err)
	}
	if !s.IsIgnored(42) {
		t.Fatal("not ignored")
	}
	if _, err := s.Add(42, "Z", "", "guest"); err != nil {
		t.Fatal(err)
	}
	if s.IsIgnored(42) {
		t.Fatal("still ignored after add")
	}
}
