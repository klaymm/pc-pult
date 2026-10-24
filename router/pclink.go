package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const (
	lostAfter = 45 * time.Second
	staleItem = 60 * time.Second
)

type UserInfo struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Role   string   `json:"role"`
	Perms  []string `json:"perms"`
	Notify bool     `json:"notify"`
	Owner  bool     `json:"owner"`
}

func userInfo(u *User) UserInfo {
	return UserInfo{ID: u.ID, Name: u.Name, Role: u.Role(), Perms: u.EffectivePerms(), Notify: u.Notify, Owner: u.Owner}
}

type Item struct {
	Update json.RawMessage `json:"update"`
	User   UserInfo        `json:"user"`
	Panel  int             `json:"panel"`

	at time.Time
}

type Hello struct {
	Version string `json:"version"`
	Host    string `json:"host"`
	Fresh   bool   `json:"fresh"`
	Session string `json:"session"`
}

type LinkEvent struct {
	Kind   string
	Reason string
	By     int64
	Fresh  bool
	Lost   bool
}

type PCLink struct {
	mu       sync.Mutex
	now      func() time.Time
	queue    []Item
	signal   chan struct{}
	online   bool
	greeted  bool
	lost     bool
	polls    int
	lastSeen time.Time
	hello    Hello
	events   chan LinkEvent
}

func newPCLink() *PCLink {
	return &PCLink{now: time.Now, signal: make(chan struct{}), events: make(chan LinkEvent, 16)}
}

func (l *PCLink) emit(ev LinkEvent) {
	select {
	case l.events <- ev:
	default:
	}
}

func (l *PCLink) Online() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.online
}

func (l *PCLink) LastSeen() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeen
}

func (l *PCLink) Info() Hello {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hello
}

func (l *PCLink) Enqueue(it Item) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.online {
		return false
	}
	it.at = l.now()
	l.queue = append(l.queue, it)
	close(l.signal)
	l.signal = make(chan struct{})
	return true
}

func (l *PCLink) Hello(h Hello) {
	l.mu.Lock()
	wasOnline, wasLost := l.online, l.lost
	l.greeted, l.online, l.lost = true, true, false
	l.hello = h
	l.lastSeen = l.now()
	l.mu.Unlock()
	if !wasOnline {
		l.emit(LinkEvent{Kind: "online", Fresh: h.Fresh, Lost: wasLost})
	}
}

func (l *PCLink) Bye(reason string, by int64) {
	l.mu.Lock()
	wasOnline := l.online
	l.greeted, l.online, l.lost = false, false, false
	l.queue = nil
	l.lastSeen = l.now()
	l.mu.Unlock()
	if wasOnline {
		l.emit(LinkEvent{Kind: "bye", Reason: reason, By: by})
	}
}

func (l *PCLink) Poll(ctx context.Context, timeout time.Duration) ([]Item, bool) {
	l.mu.Lock()
	if !l.greeted {
		l.mu.Unlock()
		return nil, false
	}
	l.polls++
	l.lastSeen = l.now()
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		l.polls--
		l.lastSeen = l.now()
		l.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		l.mu.Lock()
		if !l.greeted {
			l.mu.Unlock()
			return nil, false
		}
		if len(l.queue) > 0 {
			cutoff := l.now().Add(-staleItem)
			var out []Item
			for _, it := range l.queue {
				if it.at.After(cutoff) {
					out = append(out, it)
				}
			}
			l.queue = nil
			l.mu.Unlock()
			if len(out) > 0 {
				return out, true
			}
			continue
		}
		sig := l.signal
		l.mu.Unlock()
		select {
		case <-sig:
		case <-timer.C:
			return nil, true
		case <-ctx.Done():
			return nil, true
		}
	}
}

func (l *PCLink) Check() {
	l.mu.Lock()
	expired := l.online && l.polls == 0 && l.now().Sub(l.lastSeen) > lostAfter
	if expired {
		l.online, l.greeted, l.lost = false, false, true
		l.queue = nil
	}
	l.mu.Unlock()
	if expired {
		l.emit(LinkEvent{Kind: "lost"})
	}
}

func (l *PCLink) Watch(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Check()
		}
	}
}
