package main

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"sync"
	"time"
)

// Formatting

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func esc(s string) string { return html.EscapeString(s) }

func plural(n int64, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

func humanDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d с", s)
	case s < 3600:
		return fmt.Sprintf("%d мин", s/60)
	case s < 86400:
		if m := s % 3600 / 60; m > 0 {
			return fmt.Sprintf("%d ч %d мин", s/3600, m)
		}
		return fmt.Sprintf("%d ч", s/3600)
	}
	days := s / 86400
	if h := s % 86400 / 3600; h > 0 {
		return fmt.Sprintf("%d %s %d ч", days, plural(days, "день", "дня", "дней"), h)
	}
	return fmt.Sprintf("%d %s", days, plural(days, "день", "дня", "дней"))
}

func humanTime(t, now time.Time, loc *time.Location) string {
	t, now = t.In(loc), now.In(loc)
	day := func(x time.Time) time.Time {
		y, m, d := x.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	switch int(day(now).Sub(day(t)).Hours()/24 + 0.5) {
	case 0:
		return "сегодня в " + t.Format("15:04")
	case 1:
		return "вчера в " + t.Format("15:04")
	}
	return t.Format("02.01 в 15:04")
}

// Logging

type rotator struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func newRotator(path string, max int64) (*rotator, error) {
	r := &rotator{path: path, max: max}
	return r, r.open()
}

func (r *rotator) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > r.max {
		r.f.Close()
		_ = os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}
