package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const keyHeader = "X-PcPult-Key"

var blockedMethods = map[string]bool{
	"getupdates":     true,
	"setwebhook":     true,
	"deletewebhook":  true,
	"getwebhookinfo": true,
	"logout":         true,
	"close":          true,
}

type Server struct {
	cfg     *Config
	link    *PCLink
	store   *Store
	audit   *Audit
	proxy   *httputil.ReverseProxy
	api     *url.URL
	arpPath string
	panels  *Panels
}

func newServer(cfg *Config, link *PCLink, store *Store, audit *Audit, tr http.RoundTripper) (*Server, error) {
	api, err := url.Parse(cfg.TGAPI)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, link: link, store: store, audit: audit, api: api, arpPath: "/proc/net/arp", panels: openPanels("")}
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(api)
			p.Out.Header.Del(keyHeader)
		},
		Transport:     tr,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy: %s", strings.ReplaceAll(err.Error(), cfg.Token, "<token>"))
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pc/poll", s.poll)
	mux.HandleFunc("POST /pc/hello", s.hello)
	mux.HandleFunc("POST /pc/bye", s.bye)
	mux.HandleFunc("GET /pc/recipients", s.recipients)
	mux.HandleFunc("POST /pc/audit", s.auditAdd)
	mux.HandleFunc("POST /pc/panel", s.panelSet)
	mux.HandleFunc("/", s.tgProxy)
	return s.auth(mux)
}

func (s *Server) allowed(ip string) bool {
	if s.cfg.PCIP != "" {
		return ip == s.cfg.PCIP
	}
	return arpMAC(s.arpPath, ip) == s.cfg.mac.String()
}

func arpMAC(path, ip string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == ip {
			return strings.ToLower(f[3])
		}
	}
	return ""
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !s.allowed(host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		key := r.Header.Get(keyHeader)
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.PCKey)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// PC link

func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	timeout := 25 * time.Second
	if v, err := strconv.Atoi(r.URL.Query().Get("timeout")); err == nil && v >= 0 && v <= 50 {
		timeout = time.Duration(v) * time.Second
	}
	items, ok := s.link.Poll(r.Context(), timeout)
	if !ok {
		http.Error(w, "hello required", http.StatusConflict)
		return
	}
	if items == nil {
		items = []Item{}
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	var h Hello
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.link.Hello(h)
	writeJSON(w, map[string]any{"ok": true, "pc_name": s.cfg.PCName, "owner_id": s.cfg.OwnerID})
}

func (s *Server) bye(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Reason string `json:"reason"`
		By     int64  `json:"by"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	s.link.Bye(b.Reason, b.By)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) recipients(w http.ResponseWriter, r *http.Request) {
	perm := r.URL.Query().Get("perm")
	ids := []int64{}
	for _, u := range s.store.Recipients(perm) {
		ids = append(ids, u.ID)
	}
	writeJSON(w, map[string]any{"ids": ids})
}

func (s *Server) auditAdd(w http.ResponseWriter, r *http.Request) {
	var a struct {
		UserID int64  `json:"user_id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil || a.Action == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	name := "ПК"
	if u := s.store.Get(a.UserID); u != nil {
		name = u.Name
	}
	s.audit.Add(a.UserID, name, a.Action)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) panelSet(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ChatID int64 `json:"chat_id"`
		MsgID  int   `json:"msg_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.ChatID == 0 {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.panels.Set(p.ChatID, p.MsgID)
	writeJSON(w, map[string]any{"ok": true})
}

// Telegram proxy

func proxyPath(path, token string) (string, bool) {
	switch {
	case strings.HasPrefix(path, "/file/bot"):
		rest := strings.TrimPrefix(path, "/file/bot")
		i := strings.IndexByte(rest, '/')
		if i < 0 || i == len(rest)-1 {
			return "", false
		}
		return "/file/bot" + token + rest[i:], true
	case strings.HasPrefix(path, "/bot"):
		rest := strings.TrimPrefix(path, "/bot")
		i := strings.IndexByte(rest, '/')
		if i < 0 || i == len(rest)-1 {
			return "", false
		}
		method := rest[i+1:]
		if strings.Contains(method, "/") || blockedMethods[strings.ToLower(method)] {
			return "", false
		}
		return "/bot" + token + "/" + method, true
	}
	return "", false
}

func (s *Server) tgProxy(w http.ResponseWriter, r *http.Request) {
	p, ok := proxyPath(r.URL.Path, s.cfg.Token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = p
	r2.URL.RawPath = ""
	s.proxy.ServeHTTP(w, r2)
}
