package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTG struct {
	mu    sync.Mutex
	calls []fakeCall
	srv   *httptest.Server
}

type fakeCall struct {
	Method string
	Params map[string]any
}

func newFakeTG(t *testing.T) *fakeTG {
	f := &fakeTG{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{method, p})
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":100}}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTG) find(method string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func newTestBot(t *testing.T) (*Bot, *fakeTG) {
	t.Helper()
	f := newFakeTG(t)
	mac, _ := net.ParseMAC("AA-BB-CC-DD-EE-01")
	cfg := &Config{OwnerID: 1, PCName: "ПК", Broadcast: "127.0.0.1:9", mac: mac, loc: time.UTC}
	b := newBot(cfg, newTG(f.srv.URL, "TOK", http.DefaultTransport), newTestStore(t), newPCLink(), openAudit(""))
	b.username = "test_bot"
	return b, f
}

func msgUpdate(id int64, from int64, text string) Update {
	raw := `{"update_id":` + itoa(id) + `,"message":{"message_id":5,"from":{"id":` + itoa(from) + `,"first_name":"T"},"chat":{"id":` + itoa(from) + `,"type":"private"},"text":` + jsonStr(text) + `}}`
	var u Update
	_ = json.Unmarshal([]byte(raw), &u)
	u.Raw = json.RawMessage(raw)
	return u
}

func cbUpdate(id int64, from int64, data string) Update {
	raw := `{"update_id":` + itoa(id) + `,"callback_query":{"id":"cb1","from":{"id":` + itoa(from) + `,"first_name":"T"},"message":{"message_id":9,"chat":{"id":` + itoa(from) + `,"type":"private"}},"data":` + jsonStr(data) + `}}`
	var u Update
	_ = json.Unmarshal([]byte(raw), &u)
	u.Raw = json.RawMessage(raw)
	return u
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestParseCommand(t *testing.T) {
	cases := map[string][2]string{
		"/start inv_abc":    {"/start", "inv_abc"},
		"/Menu@test_bot":    {"/menu", ""},
		"hello":             {"", ""},
		"/on   ":            {"/on", ""},
		"/files@bot photos": {"/files", "photos"},
	}
	for in, want := range cases {
		c, a := parseCommand(in)
		if c != want[0] || a != want[1] {
			t.Errorf("%q: %q %q", in, c, a)
		}
	}
}

func TestOfflineMenu(t *testing.T) {
	b, f := newTestBot(t)
	b.handle(context.Background(), msgUpdate(1, 1, "/menu"))
	sends := f.find("sendMessage")
	if len(sends) != 1 || !strings.Contains(sends[0].Params["text"].(string), "выключен") {
		t.Fatalf("sends: %+v", sends)
	}
}

func waitCalls(t *testing.T, f *fakeTG, method string, n int) []fakeCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.find(method); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f.find(method)
}

func TestPanelReplacesOldAndDeletesCommand(t *testing.T) {
	b, f := newTestBot(t)
	b.panels.Set(1, 77)
	b.handle(context.Background(), msgUpdate(1, 1, "/menu"))
	dels := waitCalls(t, f, "deleteMessage", 2)
	ids := map[float64]bool{}
	for _, d := range dels {
		ids[d.Params["message_id"].(float64)] = true
	}
	if !ids[5] || !ids[77] {
		t.Fatalf("deleted: %+v", dels)
	}
	if got := b.panels.Get(1); got != 100 {
		t.Fatalf("panel = %d", got)
	}
}

func TestCallbackAdoptsPanel(t *testing.T) {
	b, f := newTestBot(t)
	b.panels.Set(1, 50)
	b.handle(context.Background(), cbUpdate(1, 1, "r:set"))
	if got := b.panels.Get(1); got != 9 {
		t.Fatalf("panel = %d", got)
	}
	dels := waitCalls(t, f, "deleteMessage", 1)
	if len(dels) != 1 || dels[0].Params["message_id"].(float64) != 50 {
		t.Fatalf("deleted: %+v", dels)
	}
}

func TestPlainTextDeletedAndForwardedWithPanel(t *testing.T) {
	b, f := newTestBot(t)
	b.link.Hello(Hello{})
	<-b.link.events
	b.panels.Set(1, 33)
	b.handle(context.Background(), msgUpdate(1, 1, "hello there"))
	items, _ := b.link.Poll(context.Background(), 0)
	if len(items) != 1 || items[0].Panel != 33 {
		t.Fatalf("items: %+v", items)
	}
	if len(waitCalls(t, f, "deleteMessage", 1)) != 1 {
		t.Fatal("plain text was not deleted")
	}
}

func TestStaleNoticeRemovedWhenPCReturns(t *testing.T) {
	b, f := newTestBot(t)
	b.onLinkEvent(context.Background(), LinkEvent{Kind: "bye", Reason: "stop"})
	if len(f.find("sendMessage")) != 1 {
		t.Fatalf("sends: %+v", f.find("sendMessage"))
	}
	b.onLinkEvent(context.Background(), LinkEvent{Kind: "online"})
	dels := f.find("deleteMessage")
	if len(dels) != 1 || dels[0].Params["message_id"].(float64) != 100 {
		t.Fatalf("deleted: %+v", dels)
	}
	if len(f.find("sendMessage")) != 1 {
		t.Fatal("quiet restart must not send a new notice")
	}
}

func TestNewNoticeReplacesOld(t *testing.T) {
	b, f := newTestBot(t)
	b.onLinkEvent(context.Background(), LinkEvent{Kind: "bye", Reason: "sleep"})
	b.onLinkEvent(context.Background(), LinkEvent{Kind: "lost"})
	if len(f.find("sendMessage")) != 2 || len(f.find("deleteMessage")) != 1 {
		t.Fatalf("sends=%d deletes=%d", len(f.find("sendMessage")), len(f.find("deleteMessage")))
	}
}

func TestOnlineForward(t *testing.T) {
	b, f := newTestBot(t)
	b.link.Hello(Hello{})
	<-b.link.events
	b.handle(context.Background(), msgUpdate(1, 1, "/screen"))
	if len(f.find("sendMessage")) != 0 {
		t.Fatal("router answered instead of forwarding")
	}
	items, _ := b.link.Poll(context.Background(), 0)
	if len(items) != 1 || !items[0].User.Owner || len(items[0].User.Perms) != len(allPerms) {
		t.Fatalf("items: %+v", items)
	}
}

func TestStrangerNotifiesOwnerOnce(t *testing.T) {
	b, f := newTestBot(t)
	b.handle(context.Background(), msgUpdate(1, 99, "hi"))
	b.handle(context.Background(), msgUpdate(2, 99, "hi again"))
	sends := f.find("sendMessage")
	if len(sends) != 1 || sends[0].Params["chat_id"].(float64) != 1 {
		t.Fatalf("sends: %+v", sends)
	}
	b.link.Hello(Hello{})
	<-b.link.events
	b.handle(context.Background(), msgUpdate(3, 99, "/menu"))
	if items, _ := b.link.Poll(context.Background(), 0); len(items) != 0 {
		t.Fatal("stranger update forwarded to PC")
	}
}

func TestInviteFlow(t *testing.T) {
	b, f := newTestBot(t)
	b.handle(context.Background(), cbUpdate(1, 1, "r:ui:user"))
	edits := f.find("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("edits: %+v", edits)
	}
	text := edits[0].Params["text"].(string)
	i := strings.Index(text, "start=inv_")
	if i < 0 {
		t.Fatalf("no link in %q", text)
	}
	code := strings.TrimSuffix(text[i+len("start=inv_"):], "</code>")
	b.handle(context.Background(), msgUpdate(2, 50, "/start inv_"+code))
	u := b.store.Get(50)
	if u == nil || u.Role() != "user" {
		t.Fatalf("invitee: %+v", u)
	}
}

func TestGuestCannotManageUsers(t *testing.T) {
	b, f := newTestBot(t)
	_, _ = b.store.Add(2, "G", "", "guest")
	b.handle(context.Background(), cbUpdate(1, 2, "r:ur:1:guest"))
	answers := f.find("answerCallbackQuery")
	if len(answers) != 1 || answers[0].Params["show_alert"] != true {
		t.Fatalf("answers: %+v", answers)
	}
	if !b.store.Get(1).Owner {
		t.Fatal("owner changed")
	}
}

func TestWakeNeedsPermission(t *testing.T) {
	b, f := newTestBot(t)
	_, _ = b.store.Add(2, "G", "", "guest")
	_ = b.store.TogglePerm(2, "power_on")
	b.handle(context.Background(), cbUpdate(1, 2, "r:on"))
	answers := f.find("answerCallbackQuery")
	if len(answers) != 1 || !strings.Contains(answers[0].Params["text"].(string), "Нет прав") {
		t.Fatalf("answers: %+v", answers)
	}
}
