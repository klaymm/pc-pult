package main

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
)

type Panels struct {
	mu   sync.Mutex
	path string
	m    map[int64]int
}

func openPanels(path string) *Panels {
	p := &Panels{path: path, m: map[int64]int{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	var in map[string]int
	if json.Unmarshal(raw, &in) != nil {
		return p
	}
	for k, v := range in {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			p.m[id] = v
		}
	}
	return p
}

func (p *Panels) Get(chat int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[chat]
}

func (p *Panels) Set(chat int64, msg int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m[chat] == msg {
		return
	}
	p.m[chat] = msg
	if p.path == "" {
		return
	}
	out := make(map[string]int, len(p.m))
	for k, v := range p.m {
		out[itoa(k)] = v
	}
	if raw, err := json.Marshal(out); err == nil {
		_ = writeFileAtomic(p.path, raw, 0o600)
	}
}
