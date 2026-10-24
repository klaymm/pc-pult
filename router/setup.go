package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Constants

var telegramAPI = "https://api.telegram.org"

const (
	binPath      = "/opt/sbin/pc-pult-router"
	defaultLog   = "/opt/var/log/pc-pult.log"
	defaultCfg   = "/opt/etc/pc-pult/config.json"
	rawBase      = "https://raw.githubusercontent.com/klaymm/pc-pult/main"
	zeroMAC      = "00:00:00:00:00:00"
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pairAttempts = 5
)

const workerCode = `const TELEGRAM = "https://api.telegram.org";
const SECRET = "{{secret}}";
const encoder = new TextEncoder();

function safeEqual(a, b) {
  const x = encoder.encode(a);
  const y = encoder.encode(b);
  return x.byteLength === y.byteLength && crypto.subtle.timingSafeEqual(x, y);
}

const notFound = () => new Response("Not found", { status: 404 });

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const [, secret, ...rest] = url.pathname.split("/");
    const path = "/" + rest.join("/");

    if (!safeEqual(secret, SECRET) || !/^\/(file\/)?bot/.test(path)) return notFound();

    if (request.method !== "GET" && request.method !== "POST") {
      return new Response("Method not allowed", {
        status: 405,
        headers: { allow: "GET, POST" },
      });
    }

    const headers = new Headers();
    const type = request.headers.get("content-type");
    if (type) headers.set("content-type", type);

    try {
      return await fetch(TELEGRAM + path + url.search, {
        method: request.method,
        headers,
        body: request.method === "GET" ? undefined : request.body,
      });
    } catch {
      return new Response("Bad gateway", { status: 502 });
    }
  },
};`

func workerHelp(secret string) string {
	return `Worker - это маленькая программа в бесплатном Cloudflare, которая пересылает запросы в Telegram.
В коде ниже уже стоит секретная часть адреса, её знаете только вы, поэтому чужие Worker не используют. Сделайте так:
  1. Зайдите на dash.cloudflare.com, раздел Workers & Pages, создайте Worker (шаблон Hello World).
  2. Нажмите Edit code, замените весь код на этот и нажмите Deploy:

` + strings.ReplaceAll(workerCode, "{{secret}}", secret) + `

  3. Скопируйте адрес вида https://имя.ваш-аккаунт.workers.dev и введите его ниже без секретной части: мастер добавит её сам.
Токен бота будет проходить через ваш Cloudflare.`
}

// Setup

type tgAccess struct {
	tg    *TG
	token string
	api   string
	proxy string
	bot   string
}

type Setup struct {
	cfgPath   string
	port      string
	in        *bufio.Reader
	out       io.Writer
	secretFn  func() (string, error)
	directAPI string
	lanFn     func() (*net.IPNet, error)
	pairAddr  string
	onPair    func(addr, code string)
	newCode   func(n int) string
	newSecret func() string
	newTr     func(proxy string) (http.RoundTripper, error)
	startSvc  func() error
	stopSvc   func() error
	hasNdmc   func() bool
	claimWait time.Duration
	pairWait  time.Duration

	workerSecret string
}

func newSetup(cfgPath, port string) *Setup {
	s := &Setup{
		cfgPath:   cfgPath,
		port:      port,
		in:        bufio.NewReader(os.Stdin),
		out:       os.Stdout,
		directAPI: telegramAPI,
		newCode:   randomCode,
		newSecret: func() string { return newKey()[:32] },
		newTr:     func(proxy string) (http.RoundTripper, error) { return newTransport(proxy) },
		hasNdmc:   func() bool { _, err := exec.LookPath("ndmc"); return err == nil },
		claimWait: 15 * time.Minute,
		pairWait:  30 * time.Minute,
	}
	s.secretFn = func() (string, error) {
		sttyRun("-echo")
		defer sttyRun("echo")
		line, err := s.in.ReadString('\n')
		fmt.Fprintln(s.out)
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
		return "", err
	}
	s.startSvc = func() error { return startService(s.cfgPath, s.logFile()) }
	s.stopSvc = func() error { return stopService(s.cfgPath) }
	s.lanFn = s.detectLAN
	return s
}

func (s *Setup) say(format string, args ...any) {
	fmt.Fprintf(s.out, format+"\n", args...)
}

func (s *Setup) ask(prompt, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(s.out, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(s.out, "%s: ", prompt)
	}
	line, err := s.in.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line, nil
	}
	if err != nil {
		return "", err
	}
	return def, nil
}

// Network

func (s *Setup) detectLAN() (*net.IPNet, error) {
	if ifc, err := net.InterfaceByName("br0"); err == nil {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				return &net.IPNet{IP: n.IP.To4(), Mask: n.Mask}, nil
			}
		}
	}
	s.say("Не удалось определить адрес роутера в локальной сети.")
	raw, err := s.ask("Адрес роутера в локальной сети", "192.168.1.1")
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(raw).To4()
	if ip == nil {
		return nil, fmt.Errorf("%q не похоже на адрес IPv4", raw)
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(24, 32)}, nil
}

func broadcastAddr(n *net.IPNet) string {
	ip := n.IP.To4()
	mask := n.Mask
	if len(mask) == 16 {
		mask = mask[12:]
	}
	b := make(net.IP, 4)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return net.JoinHostPort(b.String(), "9")
}

func lanFromListen(listen string) (*net.IPNet, string) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return &net.IPNet{IP: net.IPv4(192, 168, 1, 1).To4(), Mask: net.CIDRMask(24, 32)}, "8765"
	}
	ip := net.ParseIP(host).To4()
	if ifaces, err := net.Interfaces(); err == nil && ip != nil {
		for _, ifc := range ifaces {
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
					return &net.IPNet{IP: ip, Mask: n.Mask}, port
				}
			}
		}
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(24, 32)}, port
}

// Codes

func randomCode(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])&31]
	}
	return string(b)
}

func newKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Telegram

func describeTGError(err error) string {
	var ae *APIError
	if errors.As(err, &ae) && (ae.Code == 401 || ae.Code == 404) && !strings.HasPrefix(ae.Description, badResponse) {
		return "токен не подошёл"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "нет ответа от Telegram за 30 секунд"
	}
	return err.Error()
}

func isYes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes", "д", "да":
		return true
	}
	return false
}

func (s *Setup) connectTelegram(ctx context.Context) (*tgAccess, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.say("")
		s.say("Как роутер будет выходить в Telegram?")
		s.say("  1) Telegram открывается напрямую")
		s.say("  2) Через свой Cloudflare Worker")
		s.say("  3) Через прокси (http:// или socks5://)")
		mode, err := s.ask("Ваш выбор", "1")
		if err != nil {
			return nil, err
		}
		acc := &tgAccess{}
		existing := false
		switch mode {
		case "1":
		case "2":
			answer, err := s.ask("Worker уже есть? (y/n)", "n")
			if err != nil {
				return nil, err
			}
			existing = isYes(answer)
			if existing {
				s.say("Введите адрес Worker целиком, вместе с секретной частью, если она есть.")
				s.say("Если в Worker задан список токенов (BOT_TOKEN), добавьте в него токен этого бота.")
			} else {
				if s.workerSecret == "" {
					s.workerSecret = s.newSecret()
				}
				s.say("")
				s.say("%s", workerHelp(s.workerSecret))
			}
			raw, err := s.ask("Адрес Worker", "")
			if err != nil {
				return nil, err
			}
			u, perr := url.Parse(strings.TrimSpace(raw))
			if perr != nil || u.Scheme != "https" || u.Host == "" {
				s.say("Адрес должен начинаться с https://")
				continue
			}
			path := strings.Trim(u.Path, "/")
			if path == "" && !existing {
				path = s.workerSecret
			}
			acc.api = "https://" + u.Host
			if path != "" {
				acc.api += "/" + path
			}
		case "3":
			raw, err := s.ask("Адрес прокси, например socks5://127.0.0.1:1080", "")
			if err != nil {
				return nil, err
			}
			u, perr := url.Parse(strings.TrimSpace(raw))
			if perr != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
				s.say("Нужен адрес вида http://хост:порт или socks5://хост:порт")
				continue
			}
			acc.proxy = u.String()
		default:
			s.say("Введите 1, 2 или 3.")
			continue
		}
		tr, err := s.newTr(acc.proxy)
		if err != nil {
			s.say("Адрес прокси не подошёл: %v", err)
			continue
		}
		s.say("")
		s.say("Создайте бота у @BotFather командой /newbot и вставьте токен. Ввод не отображается.")
		fmt.Fprint(s.out, "Токен бота: ")
		token, err := s.secretFn()
		if err != nil {
			return nil, err
		}
		if token == "" {
			s.say("Токен пустой.")
			continue
		}
		base := acc.api
		if base == "" {
			base = s.directAPI
		}
		acc.token, acc.tg = token, newTG(base, token, tr)
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var me TUser
		err = acc.tg.Call(cctx, "getMe", nil, &me)
		cancel()
		if err != nil {
			var ae *APIError
			notFound := errors.As(err, &ae) && ae.Code == 404
			switch {
			case mode == "2" && existing && notFound:
				s.say("Worker ответил 404. Проверьте, что в адресе есть секретная часть и что токен этого бота добавлен в список токенов Worker.")
			case mode == "2" && existing:
				s.say("Не получилось: %s. Проверьте адрес Worker.", describeTGError(err))
			case mode == "2" && notFound:
				s.say("Worker ответил 404. Проверьте, что код вставлен целиком и сохранён кнопкой Deploy, а адрес введён без лишнего.")
			case mode == "2":
				s.say("Не получилось: %s. Проверьте, что в Worker вставлен код выше и нажат Deploy.", describeTGError(err))
			default:
				s.say("Не получилось: %s.", describeTGError(err))
			}
			if mode == "1" {
				s.say("Если Telegram у вас заблокирован, выберите вариант 2 или 3.")
			}
			continue
		}
		acc.bot = me.Username
		s.say("Бот @%s найден.", me.Username)
		return acc, nil
	}
}

func (s *Setup) claimOwner(ctx context.Context, acc *tgAccess) (TUser, error) {
	payload := "setup_" + s.newCode(12)
	s.say("")
	s.say("Теперь станьте владельцем бота. Откройте ссылку и нажмите Start:")
	s.say("  https://t.me/%s?start=%s", acc.bot, payload)
	s.say("Или отправьте боту сообщение: /start %s", payload)
	s.say("Жду до %d минут (Ctrl+C - отмена).", int(s.claimWait.Minutes()))
	deadline := time.Now().Add(s.claimWait)
	var offset int64
	for ctx.Err() == nil && time.Now().Before(deadline) {
		ups, err := acc.tg.GetUpdates(ctx, offset, 20)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Code == 409 {
				return TUser{}, errors.New("у бота включён вебхук или его читает другая программа. Отключите их и запустите настройку снова")
			}
			if ctx.Err() == nil {
				s.say("Нет связи с Telegram: %v. Пробую снова.", err)
				sleepCtx(ctx, 3*time.Second)
			}
			continue
		}
		for _, u := range ups {
			m := u.Message
			if m != nil && m.From != nil && !m.From.IsBot && m.Chat.Type == "private" {
				if cmd, arg := parseCommand(m.Text); cmd == "/start" && arg == payload {
					_, _ = acc.tg.Send(ctx, m.From.ID, "✅ Вы владелец бота. Сейчас подключим компьютер.", nil)
					return *m.From, nil
				}
			}
			offset = u.UpdateID + 1
		}
	}
	if err := ctx.Err(); err != nil {
		return TUser{}, err
	}
	return TUser{}, errors.New("время вышло, никто не нажал Start. Запустите настройку снова")
}

// Config

func (s *Setup) logFile() string {
	if s.cfgPath == defaultCfg {
		return defaultLog
	}
	return filepath.Join(filepath.Dir(s.cfgPath), "pc-pult.log")
}

func (s *Setup) writeConfig(acc *tgAccess, owner TUser, pcName, zone string, lan *net.IPNet, key string) error {
	cfg := map[string]any{
		"token":     acc.token,
		"owner_id":  owner.ID,
		"pc_name":   pcName,
		"pc_mac":    zeroMAC,
		"pc_ip":     "",
		"broadcast": broadcastAddr(lan),
		"wake_cmd":  []string{},
		"listen":    net.JoinHostPort(lan.IP.String(), s.port),
		"pc_key":    key,
		"data_dir":  filepath.Dir(s.cfgPath),
		"log_file":  s.logFile(),
		"timezone":  zone,
		"tg_api":    acc.api,
		"tg_proxy":  acc.proxy,
	}
	return s.saveConfig(cfg)
}

func (s *Setup) saveConfig(cfg map[string]any) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.cfgPath), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(s.cfgPath, append(out, '\n'), 0o600)
}

func updateConfigKeys(path string, kv map[string]any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	for k, v := range kv {
		m[k] = v
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(out, '\n'), 0o600)
}

func (s *Setup) wakeCmd(mac string) []string {
	if s.hasNdmc() {
		return []string{"ndmc", "-c", "ip hotspot wake " + strings.ToLower(mac)}
	}
	return []string{}
}

// Pairing

type pairResult struct {
	MAC  net.HardwareAddr
	Host string
}

type pairHandler struct {
	lan    *net.IPNet
	code   string
	key    string
	name   string
	mu     sync.Mutex
	fails  int
	done   chan pairResult
	locked chan struct{}
}

func newPairHandler(lan *net.IPNet, code, key, name string) *pairHandler {
	return &pairHandler{lan: lan, code: code, key: key, name: name, done: make(chan pairResult, 1), locked: make(chan struct{})}
}

func (h *pairHandler) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *pairHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/pair" {
		http.NotFound(w, r)
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !h.lan.Contains(ip) {
		h.reply(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var req struct {
		Code string `json:"code"`
		MAC  string `json:"mac"`
		Host string `json:"host"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		h.reply(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fails >= pairAttempts {
		h.reply(w, http.StatusTooManyRequests, map[string]string{"error": "locked"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(req.Code))), []byte(h.code)) != 1 {
		h.fails++
		if h.fails == pairAttempts {
			close(h.locked)
		}
		h.reply(w, http.StatusForbidden, map[string]string{"error": "bad code"})
		return
	}
	mac, err := net.ParseMAC(strings.TrimSpace(req.MAC))
	if err != nil || len(mac) != 6 || mac.String() == zeroMAC || mac.String() == "ff:ff:ff:ff:ff:ff" {
		h.reply(w, http.StatusBadRequest, map[string]string{"error": "bad mac"})
		return
	}
	h.reply(w, http.StatusOK, map[string]any{"ok": true, "key": h.key, "pc_name": h.name})
	select {
	case h.done <- pairResult{MAC: mac, Host: strings.TrimSpace(req.Host)}:
	default:
	}
}

func (s *Setup) pairPC(ctx context.Context, lan *net.IPNet, port, code, key, pcName string) (*pairResult, error) {
	addr := s.pairAddr
	if addr == "" {
		addr = net.JoinHostPort(lan.IP.String(), port)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть %s: %w", addr, err)
	}
	h := newPairHandler(lan, code, key, pcName)
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	if s.onPair != nil {
		s.onPair(ln.Addr().String(), code)
	}
	skip := make(chan struct{})
	go func() {
		_, _ = s.in.ReadString('\n')
		close(skip)
	}()
	select {
	case res := <-h.done:
		return &res, nil
	case <-h.locked:
		return nil, errors.New("слишком много неверных кодов, подключение закрыто")
	case <-skip:
		return nil, nil
	case <-time.After(s.pairWait):
		return nil, errors.New("время вышло, ПК не подключился")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func pcCommand(routerIP, port, code string) string {
	return fmt.Sprintf(`powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm %s/install-pc.ps1).TrimStart([char]0xFEFF))) -Router %s:%s -Code %s"`, rawBase, routerIP, port, code)
}

func (s *Setup) connectPC(ctx context.Context, lan *net.IPNet, port, key, pcName string) (*pairResult, error) {
	code := s.newCode(8)
	s.say("")
	s.say("Теперь подключите компьютер. Откройте PowerShell на нём и вставьте команду:")
	s.say("")
	s.say("%s", pcCommand(lan.IP.String(), port, code))
	s.say("")
	s.say("Скрипт сам запросит права администратора. Жду до %d минут.", int(s.pairWait.Minutes()))
	s.say("Компьютера под рукой нет? Нажмите Enter, подключить его можно позже командой " + binPath + " -pair")
	return s.pairPC(ctx, lan, port, code, key, pcName)
}

func (s *Setup) applyPair(res *pairResult, key string) error {
	return updateConfigKeys(s.cfgPath, map[string]any{
		"pc_mac":   res.MAC.String(),
		"pc_key":   key,
		"wake_cmd": s.wakeCmd(res.MAC.String()),
	})
}

// Run

func (s *Setup) Run(ctx context.Context, force, noStart bool) error {
	if _, err := os.Stat(s.cfgPath); err == nil && !force {
		return fmt.Errorf("настройка уже есть (%s). Чтобы начать заново, добавьте -force", s.cfgPath)
	}
	s.say("Настройка pc-pult. Понадобится бот Telegram и около пяти минут.")
	lan, err := s.lanFn()
	if err != nil {
		return err
	}
	acc, err := s.connectTelegram(ctx)
	if err != nil {
		return err
	}
	pcName, err := s.ask("Как назвать компьютер в боте", "ПК")
	if err != nil {
		return err
	}
	zone, err := s.askZone()
	if err != nil {
		return err
	}
	owner, err := s.claimOwner(ctx, acc)
	if err != nil {
		return err
	}
	s.say("Владелец: %s.", owner.DisplayName())

	key := newKey()
	if err := s.writeConfig(acc, owner, pcName, zone, lan, key); err != nil {
		return fmt.Errorf("не получилось сохранить настройки: %w", err)
	}
	s.say("Настройки сохранены: %s", s.cfgPath)

	res, perr := s.connectPC(ctx, lan, s.port, key, pcName)
	if res != nil {
		if err := s.applyPair(res, key); err != nil {
			return fmt.Errorf("не получилось сохранить данные компьютера: %w", err)
		}
		s.say("Компьютер %s подключён.", res.Host)
	} else if perr != nil {
		s.say("Компьютер не подключён: %v.", perr)
	} else {
		s.say("Подключение компьютера пропущено.")
	}

	if !noStart {
		if err := s.startSvc(); err != nil {
			return fmt.Errorf("не получилось запустить службу: %w. Запустите её вручную, чтобы увидеть ошибку: %s -config %s", err, binPath, s.cfgPath)
		}
		s.say("Служба запущена.")
	}
	s.say("")
	s.say("Готово. Откройте бота: https://t.me/%s", acc.bot)
	if res == nil {
		s.say("Когда компьютер будет под рукой, выполните: " + binPath + " -pair")
	}
	s.say("Журнал: tail -f %s", s.logFile())
	return nil
}

func (s *Setup) Pair(ctx context.Context, cfg *Config, noStart bool) error {
	lan, port := lanFromListen(cfg.Listen)
	if lan.IP == nil {
		return errors.New("в настройках не указан адрес listen")
	}
	if !noStart {
		_ = s.stopSvc()
		defer func() { _ = s.startSvc() }()
	}
	key := newKey()
	res, err := s.connectPC(ctx, lan, port, key, cfg.PCName)
	if err != nil {
		return err
	}
	if res == nil {
		s.say("Подключение пропущено, прежние настройки не менялись.")
		return nil
	}
	if err := s.applyPair(res, key); err != nil {
		return fmt.Errorf("не получилось сохранить данные компьютера: %w", err)
	}
	s.say("Компьютер %s подключён.", res.Host)
	return nil
}
