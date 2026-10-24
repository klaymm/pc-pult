package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Fakes

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type setupUpdate struct {
	id  int64
	raw string
}

type setupTG struct {
	good    string
	srv     *httptest.Server
	mu      sync.Mutex
	updates []setupUpdate
	sent    []string
}

func newSetupTG(t *testing.T, good string) *setupTG {
	t.Helper()
	f := &setupTG{good: good}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *setupTG) addMessage(id, from int64, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, setupUpdate{id: id, raw: fmt.Sprintf(
		`{"update_id":%d,"message":{"message_id":1,"from":{"id":%d,"is_bot":false,"first_name":"User%d"},"chat":{"id":%d,"type":"private"},"text":%q}}`,
		id, from, from, from, text)})
}

func (f *setupTG) sentMessages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *setupTG) handle(w http.ResponseWriter, r *http.Request) {
	token, method, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
	w.Header().Set("Content-Type", "application/json")
	if token != f.good {
		fmt.Fprint(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	switch method {
	case "getMe":
		fmt.Fprint(w, `{"ok":true,"result":{"id":99,"is_bot":true,"username":"test_bot"}}`)
	case "getUpdates":
		var p struct {
			Offset int64 `json:"offset"`
		}
		_ = json.Unmarshal(body, &p)
		f.mu.Lock()
		var out []string
		for _, u := range f.updates {
			if u.id >= p.Offset {
				out = append(out, u.raw)
			}
		}
		f.mu.Unlock()
		if len(out) == 0 {
			time.Sleep(30 * time.Millisecond)
		}
		fmt.Fprintf(w, `{"ok":true,"result":[%s]}`, strings.Join(out, ","))
	case "sendMessage":
		f.mu.Lock()
		f.sent = append(f.sent, string(body))
		f.mu.Unlock()
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":5}}`)
	default:
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	}
}

// Harness

type harness struct {
	s       *Setup
	out     *syncBuf
	tg      *setupTG
	pw      *io.PipeWriter
	pair    chan string
	started atomic.Int32
	stopped atomic.Int32
}

func newHarness(t *testing.T, token string) *harness {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	h := &harness{out: &syncBuf{}, tg: newSetupTG(t, token), pw: pw, pair: make(chan string, 1)}
	s := newSetup(filepath.Join(t.TempDir(), "config.json"), "8765")
	s.in = bufio.NewReader(pr)
	s.out = h.out
	s.secretFn = func() (string, error) {
		line, err := s.in.ReadString('\n')
		return strings.TrimSpace(line), err
	}
	s.directAPI = h.tg.srv.URL
	s.lanFn = func() (*net.IPNet, error) {
		return &net.IPNet{IP: net.IPv4(127, 0, 0, 1).To4(), Mask: net.CIDRMask(8, 32)}, nil
	}
	s.pairAddr = "127.0.0.1:0"
	s.onPair = func(addr, _ string) { h.pair <- addr }
	var mu sync.Mutex
	codes := []string{"OWNCODE12345", "PAIRCODE"}
	s.newCode = func(int) string {
		mu.Lock()
		defer mu.Unlock()
		c := codes[0]
		if len(codes) > 1 {
			codes = codes[1:]
		}
		return c
	}
	s.startSvc = func() error { h.started.Add(1); return nil }
	s.stopSvc = func() error { h.stopped.Add(1); return nil }
	s.hasNdmc = func() bool { return true }
	s.claimWait = 5 * time.Second
	s.pairWait = 5 * time.Second
	h.s = s
	return h
}

func (h *harness) type_(text string) {
	go func() { _, _ = h.pw.Write([]byte(text)) }()
}

func (h *harness) run(t *testing.T, force, noStart bool) chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx, force, noStart) }()
	return done
}

func (h *harness) waitPair(t *testing.T) string {
	t.Helper()
	select {
	case addr := <-h.pair:
		return addr
	case <-time.After(8 * time.Second):
		t.Fatalf("pairing server did not start, output:\n%s", h.out.String())
	}
	return ""
}

func waitDone(t *testing.T, done chan error, h *harness) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("setup did not finish, output:\n%s", h.out.String())
	}
	return nil
}

func postPair(t *testing.T, addr, code, mac string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{"code":%q,"mac":%q,"host":"DESKTOP-1"}`, code, mac)
	resp, err := http.Post("http://"+addr+"/pair", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func readConfigMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Wizard

func TestSetupFullFlow(t *testing.T) {
	h := newHarness(t, "GOOD:TOKEN")
	h.tg.addMessage(10, 888, "/start setup_WRONG")
	h.tg.addMessage(11, 777, "/start setup_OWNCODE12345")
	h.type_("1\nGOOD:TOKEN\nМой ПК\nEurope/Moscow\n")
	done := h.run(t, false, false)

	addr := h.waitPair(t)
	if code, _ := postPair(t, addr, "NOPE", "AA-BB-CC-DD-EE-02"); code != http.StatusForbidden {
		t.Fatalf("wrong code: %d", code)
	}
	code, resp := postPair(t, addr, "paircode", "AA-BB-CC-DD-EE-02")
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("pair: %d %v", code, resp)
	}
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}

	m := readConfigMap(t, h.s.cfgPath)
	if m["owner_id"] != float64(777) || m["token"] != "GOOD:TOKEN" || m["pc_name"] != "Мой ПК" {
		t.Fatalf("config: %v", m)
	}
	if m["pc_mac"] != "aa:bb:cc:dd:ee:02" || m["pc_key"] != resp["key"] || len(m["pc_key"].(string)) < 32 {
		t.Fatalf("pairing data: %v", m)
	}
	if m["broadcast"] != "127.255.255.255:9" || m["listen"] != "127.0.0.1:8765" || m["tg_api"] != "" || m["tg_proxy"] != "" {
		t.Fatalf("network: %v", m)
	}
	wake, _ := json.Marshal(m["wake_cmd"])
	if string(wake) != `["ndmc","-c","ip hotspot wake aa:bb:cc:dd:ee:02"]` {
		t.Fatalf("wake_cmd: %s", wake)
	}
	if _, err := loadConfig(h.s.cfgPath); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if st, _ := os.Stat(h.s.cfgPath); st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("config permissions: %v", st.Mode())
	}
	if h.started.Load() != 1 {
		t.Fatalf("service starts: %d", h.started.Load())
	}
	sent := h.tg.sentMessages()
	if len(sent) != 1 || !strings.Contains(sent[0], `"chat_id":777`) {
		t.Fatalf("owner message: %v", sent)
	}
	out := h.out.String()
	if !strings.Contains(out, "https://t.me/test_bot?start=setup_OWNCODE12345") || !strings.Contains(out, "-Code PAIRCODE") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSetupRetriesBadToken(t *testing.T) {
	h := newHarness(t, "GOOD:TOKEN")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	h.type_("1\nBAD\n1\nGOOD:TOKEN\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	if !strings.Contains(h.out.String(), "токен не подошёл") {
		t.Fatalf("no hint about the token:\n%s", h.out.String())
	}
	if h.started.Load() != 0 {
		t.Fatal("service started with -no-start")
	}
}

func TestSetupSkipPairing(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	h.type_("1\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, false)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	m := readConfigMap(t, h.s.cfgPath)
	if m["pc_mac"] != zeroMAC {
		t.Fatalf("mac must stay empty: %v", m["pc_mac"])
	}
	if h.started.Load() != 1 || !strings.Contains(h.out.String(), "pc-pult-router -pair") {
		t.Fatalf("started=%d\n%s", h.started.Load(), h.out.String())
	}
}

func TestSetupRejectsBadInput(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	h.type_("2\nn\nhttp://plain.example\n3\nftp://proxy.example\n9\n1\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{"Адрес должен начинаться с https://", "Нужен адрес вида", "Введите 1, 2 или 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSetupAsksTimezoneAgain(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	h.type_("1\nT\nПК\nMars/Olympus\nСаратов\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	if m := readConfigMap(t, h.s.cfgPath); m["timezone"] != "Europe/Saratov" {
		t.Fatalf("timezone: %v", m["timezone"])
	}
	if !strings.Contains(h.out.String(), `Не нашёл часовой пояс "Mars/Olympus"`) {
		t.Fatalf("no retry message:\n%s", h.out.String())
	}
}

func TestResolveZone(t *testing.T) {
	good := map[string]string{
		"Europe/Saratov": "Europe/Saratov", "europe/saratov": "Europe/Saratov", "Saratov": "Europe/Saratov",
		"saratov": "Europe/Saratov", "Саратов": "Europe/Saratov", "  Москва ": "Europe/Moscow", "мск": "Europe/Moscow",
		"Yekaterinburg": "Asia/Yekaterinburg", "New York": "America/New_York", "UTC": "UTC",
		"UTC+4": "Etc/GMT-4", "utc-5": "Etc/GMT+5", "+3": "Etc/GMT-3", "МСК+1": "Etc/GMT-4", "msk-1": "Etc/GMT-2",
		"GMT+0": "UTC", "Etc/GMT-4": "Etc/GMT-4",
	}
	for in, want := range good {
		got, ok := resolveZone(in)
		if !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "  ", "Mars", "Mars/Olympus", "Local", "UTC+20", "мск+30", "12345"} {
		if got, ok := resolveZone(in); ok {
			t.Errorf("%q must not resolve, got %q", in, got)
		}
	}
}

func TestSetupKeepsExistingConfig(t *testing.T) {
	h := newHarness(t, "T")
	if err := os.WriteFile(h.s.cfgPath, []byte(`{"keep":"me"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := waitDone(t, h.run(t, false, true), h)
	if err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("err: %v", err)
	}
	raw, _ := os.ReadFile(h.s.cfgPath)
	if string(raw) != `{"keep":"me"}` {
		t.Fatalf("config changed: %s", raw)
	}
}

func TestSetupProxyConfig(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer proxy.Close()
	h.type_("3\n" + proxy.URL + "\nT\n1\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	m := readConfigMap(t, h.s.cfgPath)
	if m["tg_proxy"] != "" || m["tg_api"] != "" {
		t.Fatalf("failed proxy must not be saved: %v", m)
	}
}

// Pairing

func TestPairLockout(t *testing.T) {
	lan := &net.IPNet{IP: net.IPv4(192, 168, 1, 1).To4(), Mask: net.CIDRMask(24, 32)}
	h := newPairHandler(lan, "GOODCODE", strings.Repeat("k", 64), "ПК")
	send := func(code, mac string) int {
		req := httptest.NewRequest("POST", "/pair", strings.NewReader(fmt.Sprintf(`{"code":%q,"mac":%q}`, code, mac)))
		req.RemoteAddr = "192.168.1.50:4000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range pairAttempts {
		if c := send("BAD", "aa:bb:cc:dd:ee:01"); c != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	select {
	case <-h.locked:
	default:
		t.Fatal("handler did not lock")
	}
	if c := send("GOODCODE", "aa:bb:cc:dd:ee:01"); c != http.StatusTooManyRequests {
		t.Fatalf("after lockout: %d", c)
	}
}

func TestPairRejectsOutsideLAN(t *testing.T) {
	lan := &net.IPNet{IP: net.IPv4(192, 168, 1, 1).To4(), Mask: net.CIDRMask(24, 32)}
	h := newPairHandler(lan, "GOODCODE", strings.Repeat("k", 64), "ПК")
	req := httptest.NewRequest("POST", "/pair", strings.NewReader(`{"code":"GOODCODE","mac":"aa:bb:cc:dd:ee:01"}`))
	req.RemoteAddr = "10.0.0.5:4000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside LAN: %d", rec.Code)
	}
}

func TestPairRejectsBadMAC(t *testing.T) {
	lan := &net.IPNet{IP: net.IPv4(127, 0, 0, 1).To4(), Mask: net.CIDRMask(8, 32)}
	for _, mac := range []string{"", "zz", zeroMAC, "ff:ff:ff:ff:ff:ff", "aa:bb:cc:dd:ee:01:02:03"} {
		h := newPairHandler(lan, "GOODCODE", strings.Repeat("k", 64), "ПК")
		req := httptest.NewRequest("POST", "/pair", strings.NewReader(fmt.Sprintf(`{"code":"GOODCODE","mac":%q}`, mac)))
		req.RemoteAddr = "127.0.0.1:4000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mac %q: %d", mac, rec.Code)
		}
		select {
		case <-h.done:
			t.Fatalf("mac %q accepted", mac)
		default:
		}
	}
}

func TestPairModeUpdatesConfig(t *testing.T) {
	h := newHarness(t, "T")
	h.s.newCode = func(int) string { return "PAIRCODE" }
	old := strings.Repeat("o", 64)
	cfg := map[string]any{
		"token": "T", "owner_id": 1, "pc_name": "Дом", "pc_mac": "aa:bb:cc:dd:ee:01", "broadcast": "127.255.255.255:9",
		"listen": "127.0.0.1:8765", "pc_key": old, "wake_cmd": []string{}, "timezone": "Europe/Moscow", "custom": "stay",
	}
	if err := h.s.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(h.s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.s.Pair(context.Background(), loaded, false) }()
	addr := h.waitPair(t)
	code, resp := postPair(t, addr, "PAIRCODE", "aa:bb:cc:dd:ee:09")
	if code != http.StatusOK {
		t.Fatalf("pair: %d", code)
	}
	if err := waitDone(t, done, h); err != nil {
		t.Fatal(err)
	}
	m := readConfigMap(t, h.s.cfgPath)
	if m["pc_mac"] != "aa:bb:cc:dd:ee:09" || m["pc_key"] != resp["key"] || m["pc_key"] == old || m["custom"] != "stay" {
		t.Fatalf("config: %v", m)
	}
	if h.stopped.Load() != 1 || h.started.Load() != 1 {
		t.Fatalf("stop=%d start=%d", h.stopped.Load(), h.started.Load())
	}
}

func TestPairModeSkipKeepsConfig(t *testing.T) {
	h := newHarness(t, "T")
	cfg := map[string]any{
		"token": "T", "owner_id": 1, "pc_mac": "aa:bb:cc:dd:ee:01", "broadcast": "127.255.255.255:9",
		"listen": "127.0.0.1:8765", "pc_key": strings.Repeat("o", 64), "timezone": "Europe/Moscow",
	}
	if err := h.s.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.s.cfgPath)
	loaded, _ := loadConfig(h.s.cfgPath)
	done := make(chan error, 1)
	go func() { done <- h.s.Pair(context.Background(), loaded, false) }()
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(h.s.cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatal("skipped pairing changed the config")
	}
	if h.started.Load() != 1 {
		t.Fatal("service must be restarted after a skipped pairing")
	}
}

// Helpers

func TestBroadcastAddr(t *testing.T) {
	cases := map[string]string{
		"192.168.1.1/24": "192.168.1.255:9",
		"10.1.2.3/16":    "10.1.255.255:9",
		"172.16.5.9/20":  "172.16.15.255:9",
	}
	for cidr, want := range cases {
		ip, n, _ := net.ParseCIDR(cidr)
		n.IP = ip.To4()
		if got := broadcastAddr(n); got != want {
			t.Errorf("%s: %s, want %s", cidr, got, want)
		}
	}
}

func TestRandomCodeAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		c := randomCode(12)
		if len(c) != 12 || strings.Trim(c, codeAlphabet) != "" {
			t.Fatalf("bad code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 49 {
		t.Fatal("codes repeat")
	}
}

func TestPCCommand(t *testing.T) {
	cmd := pcCommand("192.168.1.1", "8765", "ABCD2345")
	for _, want := range []string{"install-pc.ps1", "-Router 192.168.1.1:8765", "-Code ABCD2345", "ExecutionPolicy Bypass"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("missing %q in %s", want, cmd)
		}
	}
}

func TestUpdateConfigKeysKeepsOthers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	_ = os.WriteFile(p, []byte(`{"a":1,"b":"x"}`), 0o600)
	if err := updateConfigKeys(p, map[string]any{"b": "y", "c": true}); err != nil {
		t.Fatal(err)
	}
	m := readConfigMap(t, p)
	if m["a"] != float64(1) || m["b"] != "y" || m["c"] != true {
		t.Fatalf("%v", m)
	}
}

func TestLanFromListen(t *testing.T) {
	lan, port := lanFromListen("192.168.77.1:9000")
	if lan.IP.String() != "192.168.77.1" || port != "9000" {
		t.Fatalf("%v %s", lan, port)
	}
	if ones, _ := lan.Mask.Size(); ones == 0 {
		t.Fatal("mask is empty")
	}
}

// Worker

func workerServer(t *testing.T, h *harness, secret string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret != "" {
			prefix := "/" + secret
			if !strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		}
		h.tg.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	h.s.newTr = func(string) (http.RoundTripper, error) { return srv.Client().Transport, nil }
	return srv
}

func TestSetupWorkerAddsGeneratedSecret(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	h.s.newSecret = func() string { return "GENSECRET" }
	srv := workerServer(t, h, "GENSECRET")
	h.type_("2\nn\n" + srv.URL + "\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	if m := readConfigMap(t, h.s.cfgPath); m["tg_api"] != srv.URL+"/GENSECRET" {
		t.Fatalf("tg_api: %v", m["tg_api"])
	}
	out := h.out.String()
	if !strings.Contains(out, `const SECRET = "GENSECRET";`) || strings.Contains(out, "{{secret}}") {
		t.Fatalf("worker code does not carry the secret:\n%s", out)
	}
}

func TestSetupWorkerKeepsOwnSecret(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	srv := workerServer(t, h, "mine")
	h.type_("2\ny\n" + srv.URL + "/mine/\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	if m := readConfigMap(t, h.s.cfgPath); m["tg_api"] != srv.URL+"/mine" {
		t.Fatalf("tg_api: %v", m["tg_api"])
	}
}

func TestSetupWorkerNotFoundHint(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	h.s.newSecret = func() string { return "GENSECRET" }
	srv := workerServer(t, h, "OTHER")
	h.type_("2\nn\n" + srv.URL + "\nT\n1\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	if out := h.out.String(); !strings.Contains(out, "Worker ответил 404") || strings.Contains(out, "токен не подошёл") {
		t.Fatalf("hint:\n%s", out)
	}
}

func TestSetupExistingWorkerKeepsAddressAsIs(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	srv := workerServer(t, h, "")
	h.type_("2\ny\n" + srv.URL + "\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	if m := readConfigMap(t, h.s.cfgPath); m["tg_api"] != srv.URL {
		t.Fatalf("tg_api: %v", m["tg_api"])
	}
	if out := h.out.String(); strings.Contains(out, "const SECRET") || h.s.workerSecret != "" {
		t.Fatalf("an existing Worker must not get generated code or a secret:\n%s", out)
	}
}

func TestSetupExistingWorkerNotFoundHint(t *testing.T) {
	h := newHarness(t, "T")
	h.tg.addMessage(1, 5, "/start setup_OWNCODE12345")
	srv := workerServer(t, h, "OTHER")
	h.type_("2\ny\n" + srv.URL + "\nT\n1\nT\nПК\nEurope/Moscow\n")
	done := h.run(t, false, true)
	h.waitPair(t)
	h.type_("\n")
	if err := waitDone(t, done, h); err != nil {
		t.Fatalf("run: %v\n%s", err, h.out.String())
	}
	out := h.out.String()
	if !strings.Contains(out, "добавлен в список токенов Worker") || strings.Contains(out, "кнопкой Deploy") {
		t.Fatalf("hint for an existing Worker:\n%s", out)
	}
}

func TestIsYes(t *testing.T) {
	for _, a := range []string{"y", "Y", " yes ", "д", "Да"} {
		if !isYes(a) {
			t.Errorf("%q must be yes", a)
		}
	}
	for _, a := range []string{"", "n", "нет", "no", "ok"} {
		if isYes(a) {
			t.Errorf("%q must not be yes", a)
		}
	}
}

func TestWorkerHelpCarriesSecret(t *testing.T) {
	text := workerHelp("abc123")
	for _, want := range []string{`const SECRET = "abc123";`, "export default", "api.telegram.org", "Deploy"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(text, "{{secret}}") {
		t.Error("placeholder left")
	}
}

func TestDescribeTGError(t *testing.T) {
	if got := describeTGError(&APIError{Code: 401, Description: "Unauthorized"}); got != "токен не подошёл" {
		t.Errorf("401: %q", got)
	}
	if got := describeTGError(&APIError{Code: 404, Description: badResponse + ": getMe"}); strings.Contains(got, "токен") {
		t.Errorf("non-JSON 404 blamed on the token: %q", got)
	}
}

// Service

func TestMatchesService(t *testing.T) {
	cfg := "/opt/etc/pc-pult/config.json"
	yes := [][]string{
		{"/opt/sbin/pc-pult-router", "-supervise", "-config", cfg},
		{"pc-pult-router", "-config", cfg},
		{"/opt/sbin/pc-pult-router", "-config=" + cfg},
	}
	no := [][]string{
		{"/opt/sbin/pc-pult-router", "-setup", "-config", cfg},
		{"/opt/sbin/pc-pult-router", "-pair", "-config", cfg},
		{"/opt/sbin/pc-pult-router", "-supervise", "-config", "/opt/tmp/other/config.json"},
		{"/opt/sbin/pc-pult-router", "-supervise"},
		{"/bin/sh", "-c", "pc-pult-router -config " + cfg},
		{"/opt/sbin/other-router", "-config", cfg},
		{cfg},
	}
	for _, a := range yes {
		if !matchesService(a, cfg) {
			t.Errorf("must match: %v", a)
		}
	}
	for _, a := range no {
		if matchesService(a, cfg) {
			t.Errorf("must not match: %v", a)
		}
	}
}
