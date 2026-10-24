package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMagicPacket(t *testing.T) {
	mac, _ := net.ParseMAC("AA-BB-CC-DD-EE-01")
	p := magicPacket(mac)
	if len(p) != 102 {
		t.Fatalf("len %d", len(p))
	}
	if !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatal("header")
	}
	for i := range 16 {
		if !bytes.Equal(p[6+i*6:12+i*6], mac) {
			t.Fatalf("mac copy %d", i)
		}
	}
}

func TestProxyPath(t *testing.T) {
	cases := []struct {
		in, out string
		ok      bool
	}{
		{"/bot0:pc/sendMessage", "/botTOK/sendMessage", true},
		{"/botanything/sendPhoto", "/botTOK/sendPhoto", true},
		{"/bot0:pc/getUpdates", "", false},
		{"/bot0:pc/GETUPDATES", "", false},
		{"/bot0:pc/logOut", "", false},
		{"/bot0:pc/", "", false},
		{"/bot0:pc/a/b", "", false},
		{"/file/bot0:pc/documents/file_1.txt", "/file/botTOK/documents/file_1.txt", true},
		{"/pc/poll", "", false},
	}
	for _, c := range cases {
		out, ok := proxyPath(c.in, "TOK")
		if ok != c.ok || out != c.out {
			t.Errorf("%s: got %q %v", c.in, out, ok)
		}
	}
}

func TestLinkLifecycle(t *testing.T) {
	l := newPCLink()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	if _, ok := l.Poll(context.Background(), 0); ok {
		t.Fatal("poll without hello must fail")
	}
	if l.Enqueue(Item{}) {
		t.Fatal("enqueue while offline")
	}
	l.Hello(Hello{Fresh: true})
	if ev := <-l.events; ev.Kind != "online" || !ev.Fresh {
		t.Fatalf("event %+v", ev)
	}
	if !l.Enqueue(Item{Update: json.RawMessage(`{"update_id":1}`)}) {
		t.Fatal("enqueue online")
	}
	items, ok := l.Poll(context.Background(), time.Second)
	if !ok || len(items) != 1 {
		t.Fatalf("poll: %v %v", items, ok)
	}

	now = now.Add(time.Minute)
	l.Check()
	if ev := <-l.events; ev.Kind != "lost" {
		t.Fatalf("event %+v", ev)
	}
	if l.Online() {
		t.Fatal("still online")
	}
	l.Hello(Hello{})
	if ev := <-l.events; ev.Kind != "online" || !ev.Lost || ev.Fresh {
		t.Fatalf("event %+v", ev)
	}
	l.Bye("sleep", 7)
	if ev := <-l.events; ev.Kind != "bye" || ev.Reason != "sleep" || ev.By != 7 {
		t.Fatalf("event %+v", ev)
	}
}

func TestPollWakesOnEnqueue(t *testing.T) {
	l := newPCLink()
	l.Hello(Hello{})
	<-l.events
	done := make(chan []Item)
	go func() {
		items, _ := l.Poll(context.Background(), 5*time.Second)
		done <- items
	}()
	time.Sleep(50 * time.Millisecond)
	l.Enqueue(Item{Update: json.RawMessage(`{}`)})
	select {
	case items := <-done:
		if len(items) != 1 {
			t.Fatalf("items %d", len(items))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not wake")
	}
}

func testServer(t *testing.T, upstream string) (*Server, *PCLink) {
	t.Helper()
	cfg := &Config{Token: "TOK", PCKey: strings.Repeat("k", 32), PCIP: "127.0.0.1", TGAPI: upstream, PCName: "ПК"}
	link := newPCLink()
	srv, err := newServer(cfg, link, newTestStore(t), openAudit(""), http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	return srv, link
}

func TestServerAuth(t *testing.T) {
	srv, _ := testServer(t, "http://127.0.0.1:1")
	h := srv.Handler()

	req := httptest.NewRequest("GET", "/pc/poll", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no key: %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/pc/poll", nil)
	req.RemoteAddr = "192.168.1.50:5000"
	req.Header.Set(keyHeader, strings.Repeat("k", 32))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong ip: %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/pc/poll?timeout=0", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set(keyHeader, strings.Repeat("k", 32))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("poll before hello: %d", rec.Code)
	}
}

func TestAuthByARP(t *testing.T) {
	srv, _ := testServer(t, "http://127.0.0.1:1")
	srv.cfg.PCIP = ""
	srv.cfg.mac, _ = net.ParseMAC("AA:BB:CC:DD:EE:01")
	arp := filepath.Join(t.TempDir(), "arp")
	_ = os.WriteFile(arp, []byte("IP address       HW type     Flags       HW address            Mask     Device\n"+
		"192.168.1.91     0x1         0x2         aa:bb:cc:dd:ee:01     *        br0\n"+
		"192.168.1.50     0x1         0x2         00:11:22:33:44:55     *        br0\n"), 0o600)
	srv.arpPath = arp
	if !srv.allowed("192.168.1.91") {
		t.Fatal("pc rejected")
	}
	if srv.allowed("192.168.1.50") || srv.allowed("10.0.0.1") {
		t.Fatal("stranger allowed")
	}
}

func TestProxyForwardsWithToken(t *testing.T) {
	var gotPath, gotKey, gotBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get(keyHeader)
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer up.Close()
	srv, _ := testServer(t, up.URL)

	req := httptest.NewRequest("POST", "/bot0:pc/sendMessage", strings.NewReader(`{"chat_id":1}`))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set(keyHeader, strings.Repeat("k", 32))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || gotPath != "/botTOK/sendMessage" || gotKey != "" || gotBody != `{"chat_id":1}` {
		t.Fatalf("code=%d path=%q key=%q body=%q", rec.Code, gotPath, gotKey, gotBody)
	}
}
