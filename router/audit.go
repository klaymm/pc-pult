package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

const (
	auditKeep    = 200
	auditMaxSize = 512 << 10
)

type AuditEntry struct {
	Time   time.Time `json:"t"`
	UserID int64     `json:"uid"`
	Name   string    `json:"name"`
	Action string    `json:"action"`
}

type Audit struct {
	mu      sync.Mutex
	path    string
	entries []AuditEntry
	now     func() time.Time
}

func openAudit(path string) *Audit {
	a := &Audit{path: path, now: time.Now}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var e AuditEntry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				a.entries = append(a.entries, e)
			}
		}
		f.Close()
		if len(a.entries) > auditKeep {
			a.entries = a.entries[len(a.entries)-auditKeep:]
		}
	}
	return a
}

func (a *Audit) Add(uid int64, name, action string) {
	e := AuditEntry{Time: a.now(), UserID: uid, Name: name, Action: action}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	if len(a.entries) > auditKeep {
		a.entries = a.entries[len(a.entries)-auditKeep:]
	}
	if a.path == "" {
		return
	}
	if st, err := os.Stat(a.path); err == nil && st.Size() > auditMaxSize {
		_ = os.Rename(a.path, a.path+".1")
	}
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	raw, _ := json.Marshal(e)
	_, _ = f.Write(append(raw, '\n'))
}

func (a *Audit) Recent(n int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n > len(a.entries) {
		n = len(a.entries)
	}
	out := make([]AuditEntry, n)
	for i := range n {
		out[i] = a.entries[len(a.entries)-1-i]
	}
	return out
}
