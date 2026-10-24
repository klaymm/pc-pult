package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	wakeTimeout    = 3 * time.Minute
	pendingTTL     = 5 * time.Minute
	strangerRepeat = 12 * time.Hour
	inviteTTL      = 24 * time.Hour
)

type pending struct {
	kind  string
	msgID int
	until time.Time
}

type stranger struct {
	name     string
	username string
	at       time.Time
}

type wakeWait struct {
	chatID int64
	msgID  int
	start  time.Time
}

type Bot struct {
	cfg      *Config
	tg       *TG
	store    *Store
	link     *PCLink
	audit    *Audit
	panels   *Panels
	now      func() time.Time
	username string

	mu        sync.Mutex
	pending   map[int64]pending
	strangers map[int64]stranger
	wakes     map[int64]*wakeWait
	notices   map[int64]int
}

func newBot(cfg *Config, tg *TG, store *Store, link *PCLink, audit *Audit) *Bot {
	return &Bot{
		cfg: cfg, tg: tg, store: store, link: link, audit: audit, panels: openPanels(""), now: time.Now,
		pending:   map[int64]pending{},
		strangers: map[int64]stranger{},
		wakes:     map[int64]*wakeWait{},
		notices:   map[int64]int{},
	}
}

var botCommands = []map[string]string{
	{"command": "menu", "description": "Главное меню"},
	{"command": "on", "description": "Включить ПК"},
	{"command": "off", "description": "Выключить ПК"},
	{"command": "status", "description": "Состояние ПК"},
	{"command": "screen", "description": "Скриншот"},
	{"command": "anydesk", "description": "AnyDesk"},
	{"command": "files", "description": "Файлы"},
	{"command": "settings", "description": "Настройки и пользователи"},
	{"command": "help", "description": "Справка"},
}

// Loop

func (b *Bot) Run(ctx context.Context) {
	b.init(ctx)
	go b.linkEvents(ctx)
	go b.wakeWatch(ctx)
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		ups, err := b.tg.GetUpdates(ctx, offset, 50)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var ae *APIError
			if errors.As(err, &ae) && ae.Code == 409 {
				log.Printf("getUpdates: another program is using this bot token")
			} else {
				log.Printf("getUpdates: %v", err)
			}
			sleepCtx(ctx, backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			offset = u.UpdateID + 1
			b.safeHandle(ctx, u)
		}
	}
}

func (b *Bot) init(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		var me TUser
		err := b.tg.Call(ctx, "getMe", nil, &me)
		if err == nil {
			b.username = me.Username
			log.Printf("bot @%s", me.Username)
			break
		}
		log.Printf("getMe: %v", err)
		sleepCtx(ctx, backoff)
		backoff = min(backoff*2, 60*time.Second)
	}
	if err := b.tg.Call(ctx, "setMyCommands", map[string]any{"commands": botCommands}, nil); err != nil {
		log.Printf("setMyCommands: %v", err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (b *Bot) safeHandle(ctx context.Context, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in update %d: %v", u.UpdateID, r)
		}
	}()
	b.handle(ctx, u)
}

func parseCommand(text string) (string, string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	cmd, arg, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(cmd, "@")
	return strings.ToLower(cmd), strings.TrimSpace(arg)
}

// Routing

func (b *Bot) handle(ctx context.Context, u Update) {
	var from *TUser
	var chat *Chat
	switch {
	case u.CallbackQuery != nil:
		from = &u.CallbackQuery.From
		if u.CallbackQuery.Message == nil {
			_ = b.tg.Answer(ctx, u.CallbackQuery.ID, "", false)
			return
		}
		chat = &u.CallbackQuery.Message.Chat
	case u.Message != nil && u.Message.From != nil:
		from, chat = u.Message.From, &u.Message.Chat
	default:
		return
	}
	if chat.Type != "private" || from.IsBot {
		return
	}
	user := b.store.Get(from.ID)
	if user == nil {
		b.handleStranger(ctx, u, from)
		return
	}
	b.store.Touch(from.ID, from.DisplayName(), from.Username)
	user.Name, user.Username = from.DisplayName(), from.Username
	if u.CallbackQuery != nil {
		b.handleCallback(ctx, u, user, u.CallbackQuery)
		return
	}
	b.handleMessage(ctx, u, user, u.Message)
}

func (b *Bot) forward(u Update, user *User, chatID int64) bool {
	return b.link.Enqueue(Item{Update: u.Raw, User: userInfo(user), Panel: b.panels.Get(chatID)})
}

// Panel

func (b *Bot) deleteQuiet(ctx context.Context, chatID int64, msgID int) {
	if msgID != 0 {
		_ = b.tg.Delete(ctx, chatID, msgID)
	}
}

func (b *Bot) newPanel(ctx context.Context, chatID int64, text string, k *KB) {
	old := b.panels.Get(chatID)
	id, err := b.tg.Send(ctx, chatID, text, k)
	if err != nil {
		log.Printf("send: %v", err)
		return
	}
	b.panels.Set(chatID, id)
	if old != id {
		b.deleteQuiet(ctx, chatID, old)
	}
}

func (b *Bot) adoptPanel(ctx context.Context, chatID int64, msgID int) {
	old := b.panels.Get(chatID)
	if old == msgID {
		return
	}
	b.panels.Set(chatID, msgID)
	b.deleteQuiet(ctx, chatID, old)
}

func (b *Bot) handleMessage(ctx context.Context, u Update, user *User, m *Message) {
	cmd, arg := parseCommand(m.Text)
	chatID := m.Chat.ID
	if cmd == "" {
		if p, ok := b.takePending(user.ID); ok {
			b.deleteQuiet(ctx, chatID, m.MessageID)
			b.handlePending(ctx, user, m, p)
			return
		}
	} else {
		b.clearPending(user.ID)
		go b.deleteQuiet(ctx, chatID, m.MessageID)
	}
	switch cmd {
	case "/start":
		if strings.HasPrefix(arg, "inv_") {
			_, _ = b.tg.Send(ctx, chatID, "Вы уже есть в списке пользователей, приглашение не нужно.", nil)
		}
	case "/on":
		b.wake(ctx, user, chatID, 0, "")
		return
	case "/settings", "/users":
		text, k := b.settings(user)
		b.newPanel(ctx, chatID, text, k)
		return
	case "/log":
		if user.Owner {
			text, k := b.logView()
			b.newPanel(ctx, chatID, text, k)
			return
		}
	}
	plainText := cmd == "" && m.Text != ""
	if b.forward(u, user, chatID) {
		if plainText {
			go b.deleteQuiet(ctx, chatID, m.MessageID)
		}
		return
	}
	if plainText {
		go b.deleteQuiet(ctx, chatID, m.MessageID)
	}
	text, k := b.home(user)
	b.newPanel(ctx, chatID, text, k)
}

func (b *Bot) handleCallback(ctx context.Context, u Update, user *User, cq *CallbackQuery) {
	if cq.Data == "x" {
		_ = b.tg.Answer(ctx, cq.ID, "", false)
		return
	}
	b.adoptPanel(ctx, cq.Message.Chat.ID, cq.Message.MessageID)
	if strings.HasPrefix(cq.Data, "r:") {
		b.routerCallback(ctx, user, cq)
		return
	}
	b.clearPending(user.ID)
	if b.forward(u, user, cq.Message.Chat.ID) {
		return
	}
	toast := b.cfg.PCName + " выключен"
	if cq.Data == "menu" {
		toast = ""
	}
	_ = b.tg.Answer(ctx, cq.ID, toast, false)
	text, k := b.home(user)
	_ = b.tg.Edit(ctx, cq.Message.Chat.ID, cq.Message.MessageID, text, k)
}

// Pending input

func (b *Bot) setPending(uid int64, kind string, msgID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[uid] = pending{kind: kind, msgID: msgID, until: b.now().Add(pendingTTL)}
}

func (b *Bot) clearPending(uid int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, uid)
}

func (b *Bot) takePending(uid int64) (pending, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[uid]
	if !ok || b.now().After(p.until) {
		delete(b.pending, uid)
		return pending{}, false
	}
	delete(b.pending, uid)
	return p, true
}

func (b *Bot) handlePending(ctx context.Context, user *User, m *Message, p pending) {
	switch p.kind {
	case "addid":
		if !user.Has("users") {
			return
		}
		var id int64
		name, username := "", ""
		if fo := m.ForwardOrigin; fo != nil && fo.SenderUser != nil {
			id, name, username = fo.SenderUser.ID, fo.SenderUser.DisplayName(), fo.SenderUser.Username
		} else {
			id, _ = strconv.ParseInt(strings.TrimSpace(m.Text), 10, 64)
		}
		show := func(text string, k *KB) { b.editOrSend(ctx, m.Chat.ID, p.msgID, text, k) }
		if id <= 0 {
			b.setPending(user.ID, "addid", p.msgID)
			show("🆔 <b>Добавить по id</b>\n\nНе похоже на id. Пришлите число, например <code>123456789</code>, или перешлите сюда любое сообщение этого человека.", kb(row(cb("Отмена", "r:u"))))
			return
		}
		nu, err := b.store.Add(id, name, username, "guest")
		switch {
		case errors.Is(err, errExists):
			text, k := b.userCard(user, id)
			show("Этот человек уже в списке.\n\n"+text, k)
			return
		case err != nil:
			log.Printf("add user: %v", err)
			show("Не получилось сохранить пользователя.", kb(row(cb("⬅️ К списку", "r:u"))))
			return
		}
		b.audit.Add(user.ID, user.Name, "Добавил пользователя "+nu.Name+" (Гость)")
		text, k := b.userCard(user, id)
		show("✅ Добавлен как гость. Пусть напишет боту /start.\n\n"+text, k)
	}
}

// Screens

func (b *Bot) home(user *User) (string, *KB) {
	pc := esc(b.cfg.PCName)
	if b.link.Online() {
		return "🟢 <b>" + pc + " на связи</b>", kb(
			row(cb("📋 Открыть меню", "menu")),
			row(cb("⚙️ Настройки", "r:set")),
		)
	}
	now := b.now()
	var sb strings.Builder
	sb.WriteString("🔴 <b>" + pc + " выключен</b>\n")
	if ls := b.link.LastSeen(); !ls.IsZero() {
		fmt.Fprintf(&sb, "На связи был %s (%s назад)", humanTime(ls, now, b.cfg.loc), humanDur(now.Sub(ls)))
	} else {
		sb.WriteString("После запуска бота ПК ещё не выходил на связь")
	}
	var on []Btn
	if user.Has("power_on") {
		on = row(cb("⚡ Включить", "r:on"))
	}
	return sb.String(), kb(on, row(cb("🔄 Обновить", "r:home"), cb("⚙️ Настройки", "r:set")))
}

func (b *Bot) settings(user *User) (string, *KB) {
	role := user.Role()
	notify := "выключены"
	notifyBtn := "🔕 Уведомления: выкл"
	if user.Notify {
		notify = "включены"
		notifyBtn = "🔔 Уведомления: вкл"
	}
	text := fmt.Sprintf("⚙️ <b>Настройки</b>\n\nВы: %s\nРоль: %s %s\nУведомления: %s", esc(user.Name), roleIcons[role], roleTitles[role], notify)
	var users, logs []Btn
	if user.Has("users") {
		users = row(cb("👥 Пользователи", "r:u"))
	}
	if user.Owner {
		logs = row(cb("📜 Журнал действий", "r:log"))
	}
	return text, kb(row(cb(notifyBtn, "r:mn")), users, logs, row(cb("⬅️ Меню", "menu")))
}

func (b *Bot) logView() (string, *KB) {
	entries := b.audit.Recent(20)
	var sb strings.Builder
	sb.WriteString("📜 <b>Журнал действий</b>\n")
	if len(entries) == 0 {
		sb.WriteString("\nПока пусто.")
	}
	for _, e := range entries {
		fmt.Fprintf(&sb, "\n<i>%s</i> %s: %s", e.Time.In(b.cfg.loc).Format("02.01 15:04"), esc(e.Name), esc(e.Action))
	}
	return sb.String(), kb(row(cb("🔄 Обновить", "r:log"), cb("⬅️ Назад", "r:set")))
}

func (b *Bot) usersList() (string, *KB) {
	users := b.store.List()
	text := fmt.Sprintf("👥 <b>Пользователи</b>: %d\n\nНажмите на человека, чтобы изменить права.", len(users))
	k := kb()
	for _, u := range users {
		role := u.Role()
		k.Rows = append(k.Rows, row(cb(roleIcons[role]+" "+u.Name+" · "+roleTitles[role], "r:u:"+itoa(u.ID))))
	}
	k.Rows = append(k.Rows,
		row(cb("➕ Пригласить", "r:ui"), cb("🆔 Добавить по id", "r:ua")),
		row(cb("⬅️ Назад", "r:set")),
	)
	return text, k
}

func (b *Bot) editBlock(actor *User, target *User, perm string) string {
	switch {
	case target == nil:
		return "Пользователь не найден"
	case target.Owner:
		return "Владельца изменить нельзя"
	case target.ID == actor.ID:
		return "Свои права менять нельзя"
	case !actor.Owner && (target.Has("users") || perm == "users"):
		return "Это может только владелец"
	}
	return ""
}

func (b *Bot) userCard(actor *User, id int64) (string, *KB) {
	t := b.store.Get(id)
	if t == nil {
		return "Пользователь не найден.", kb(row(cb("⬅️ К списку", "r:u")))
	}
	role := t.Role()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s <b>%s</b>", roleIcons[role], esc(t.Name))
	if t.Username != "" {
		sb.WriteString(" @" + esc(t.Username))
	}
	fmt.Fprintf(&sb, "\nid: <code>%d</code>\nРоль: %s\nУведомления: %s\nДобавлен: %s",
		t.ID, roleTitles[role], map[bool]string{true: "включены", false: "выключены"}[t.Notify], t.Added.In(b.cfg.loc).Format("02.01.2006"))
	back := row(cb("⬅️ К списку", "r:u"))
	if b.editBlock(actor, t, "") != "" {
		sb.WriteString("\n\nПрава:")
		for _, p := range t.EffectivePerms() {
			sb.WriteString("\n• " + permTitles[p])
		}
		return sb.String(), kb(back)
	}
	sb.WriteString("\n\nНажимайте на права, чтобы включить или выключить их:")
	k := kb()
	var line []Btn
	for _, p := range allPerms {
		if p == "users" && !actor.Owner {
			continue
		}
		mark := "▫️ "
		if t.Has(p) {
			mark = "✅ "
		}
		line = append(line, cb(mark+permTitles[p], "r:up:"+itoa(t.ID)+":"+p))
		if len(line) == 2 {
			k.Rows = append(k.Rows, line)
			line = nil
		}
	}
	if len(line) > 0 {
		k.Rows = append(k.Rows, line)
	}
	var roles []Btn
	for _, r := range roleOrder {
		if r == "admin" && !actor.Owner {
			continue
		}
		title := roleTitles[r]
		if r == role {
			title = "• " + title + " •"
		}
		roles = append(roles, cb(title, "r:ur:"+itoa(t.ID)+":"+r))
	}
	notify := "🔕 Уведомления: выкл"
	if t.Notify {
		notify = "🔔 Уведомления: вкл"
	}
	k.Rows = append(k.Rows, roles,
		row(cb(notify, "r:un:"+itoa(t.ID)), cb("🗑 Удалить", "r:ud:"+itoa(t.ID))),
		back,
	)
	return sb.String(), k
}

func (b *Bot) inviteRoles(actor *User) (string, *KB) {
	text := "🔗 <b>Приглашение</b>\n\nВыберите роль. Ссылка сработает один раз и будет действовать 24 часа."
	roles := row(cb("👋 Гость", "r:ui:guest"), cb("👤 Пользователь", "r:ui:user"))
	if actor.Owner {
		roles = append(roles, cb("🛡 Админ", "r:ui:admin"))
	}
	return text, kb(roles, row(cb("⬅️ Назад", "r:u")))
}

// Callbacks

func (b *Bot) routerCallback(ctx context.Context, user *User, cq *CallbackQuery) {
	parts := strings.Split(cq.Data, ":")
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	answered := false
	answer := func(text string, alert bool) {
		if !answered {
			answered = true
			_ = b.tg.Answer(ctx, cq.ID, text, alert)
		}
	}
	defer answer("", false)
	show := func(text string, k *KB) {
		if err := b.tg.Edit(ctx, chatID, msgID, text, k); err != nil {
			log.Printf("edit: %v", err)
		}
	}
	if len(parts) < 2 {
		return
	}
	if parts[1] != "ua" {
		b.clearPending(user.ID)
	}
	switch parts[1] {
	case "home":
		show(b.home(user))
	case "on":
		answered = true
		b.wake(ctx, user, chatID, msgID, cq.ID)
	case "set":
		show(b.settings(user))
	case "mn":
		on, err := b.store.ToggleNotify(user.ID)
		if err != nil {
			answer("Не получилось", true)
			return
		}
		user.Notify = on
		show(b.settings(user))
		answer(map[bool]string{true: "Уведомления включены", false: "Уведомления выключены"}[on], false)
	case "log":
		if !user.Owner {
			answer("Журнал видит только владелец", true)
			return
		}
		show(b.logView())
	default:
		if !user.Has("users") {
			answer("Нет прав на управление пользователями", true)
			return
		}
		b.usersCallback(ctx, user, parts, msgID, answer, show)
	}
}

func (b *Bot) usersCallback(ctx context.Context, user *User, parts []string, msgID int, answer func(string, bool), show func(string, *KB)) {
	arg := func(i int) string {
		if len(parts) > i {
			return parts[i]
		}
		return ""
	}
	id, _ := strconv.ParseInt(arg(2), 10, 64)
	edit := func(perm string, fn func() error, done string) {
		if msg := b.editBlock(user, b.store.Get(id), perm); msg != "" {
			answer(msg, true)
			return
		}
		if err := fn(); err != nil {
			log.Printf("users: %v", err)
			answer("Не получилось", true)
			return
		}
		if done != "" {
			b.audit.Add(user.ID, user.Name, done)
		}
		show(b.userCard(user, id))
	}
	name := func() string {
		if t := b.store.Get(id); t != nil {
			return t.Name
		}
		return "id " + itoa(id)
	}
	switch parts[1] {
	case "u":
		if id == 0 {
			show(b.usersList())
			return
		}
		show(b.userCard(user, id))
	case "ur":
		role := arg(3)
		if role == "admin" && !user.Owner {
			answer("Админа может назначить только владелец", true)
			return
		}
		edit("", func() error { return b.store.SetRole(id, role) }, "Роль "+name()+": "+roleTitles[role])
	case "up":
		perm := arg(3)
		edit(perm, func() error { return b.store.TogglePerm(id, perm) }, "Права "+name()+": "+permTitles[perm])
	case "un":
		edit("", func() error { _, err := b.store.ToggleNotify(id); return err }, "")
	case "ud":
		if msg := b.editBlock(user, b.store.Get(id), ""); msg != "" {
			answer(msg, true)
			return
		}
		show("🗑 Удалить <b>"+esc(name())+"</b>? Бот перестанет ему отвечать.", kb(
			row(cb("Да, удалить", "r:udy:"+itoa(id)), cb("Отмена", "r:u:"+itoa(id))),
		))
	case "udy":
		if msg := b.editBlock(user, b.store.Get(id), ""); msg != "" {
			answer(msg, true)
			return
		}
		n := name()
		if err := b.store.Remove(id); err != nil {
			answer("Не получилось", true)
			return
		}
		b.audit.Add(user.ID, user.Name, "Удалил пользователя "+n)
		answer("Удалён", false)
		show(b.usersList())
	case "ui":
		role := arg(2)
		if role == "" {
			show(b.inviteRoles(user))
			return
		}
		if role == "admin" && !user.Owner {
			answer("Админа может пригласить только владелец", true)
			return
		}
		code, err := b.store.CreateInvite(role, user.ID, inviteTTL)
		if err != nil {
			answer("Не получилось", true)
			return
		}
		link := "https://t.me/" + b.username + "?start=inv_" + code
		b.audit.Add(user.ID, user.Name, "Создал приглашение ("+roleTitles[role]+")")
		share := "https://t.me/share/url?url=" + url.QueryEscape(link)
		show("🔗 <b>Приглашение: "+roleTitles[role]+"</b>\n\nОтправьте ссылку человеку. Она сработает один раз в течение 24 часов:\n\n<code>"+link+"</code>",
			kb(row(Btn{Text: "📤 Поделиться", URL: share}), row(cb("⬅️ К списку", "r:u"))))
	case "ua":
		b.setPending(user.ID, "addid", msgID)
		show("🆔 <b>Добавить по id</b>\n\nПришлите id человека числом или перешлите сюда любое его сообщение. Он будет добавлен как гость, права потом можно поменять.", kb(row(cb("Отмена", "r:u"))))
	case "ux":
		role := arg(3)
		if role == "admin" && !user.Owner {
			answer("Админа может назначить только владелец", true)
			return
		}
		b.mu.Lock()
		s := b.strangers[id]
		b.mu.Unlock()
		nu, err := b.store.Add(id, s.name, s.username, role)
		if errors.Is(err, errExists) {
			answer("Уже добавлен", false)
			show(b.userCard(user, id))
			return
		}
		if err != nil {
			answer("Не получилось", true)
			return
		}
		b.audit.Add(user.ID, user.Name, "Дал доступ "+nu.Name+" ("+roleTitles[role]+")")
		_, _ = b.tg.Send(ctx, id, "👋 Вам открыли доступ к управлению "+esc(b.cfg.PCName)+".\nРоль: "+roleTitles[role]+"\n\nОткройте меню: /menu", nil)
		answer("Доступ выдан", false)
		show(b.userCard(user, id))
	case "uxi":
		if err := b.store.Ignore(id); err != nil {
			answer("Не получилось", true)
			return
		}
		show("🚫 id <code>"+itoa(id)+"</code> в игноре. Бот больше не будет сообщать о его сообщениях.", nil)
	}
}

// Strangers

func (b *Bot) handleStranger(ctx context.Context, u Update, from *TUser) {
	if u.CallbackQuery != nil {
		_ = b.tg.Answer(ctx, u.CallbackQuery.ID, "", false)
		return
	}
	m := u.Message
	if cmd, arg := parseCommand(m.Text); cmd == "/start" && strings.HasPrefix(arg, "inv_") {
		b.redeem(ctx, from, strings.TrimPrefix(arg, "inv_"))
		return
	}
	if b.store.IsIgnored(from.ID) {
		return
	}
	now := b.now()
	b.mu.Lock()
	prev, seen := b.strangers[from.ID]
	b.strangers[from.ID] = stranger{name: from.DisplayName(), username: from.Username, at: now}
	if seen && now.Sub(prev.at) < strangerRepeat {
		b.strangers[from.ID] = prev
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("👤 <b>Боту пишет незнакомый человек</b>\n\n" + esc(from.DisplayName()))
	if from.Username != "" {
		sb.WriteString(" @" + esc(from.Username))
	}
	fmt.Fprintf(&sb, "\nid: <code>%d</code>", from.ID)
	if m.Text != "" {
		sb.WriteString("\nСообщение: " + esc(truncate(m.Text, 200)))
	}
	id := itoa(from.ID)
	for _, a := range b.store.List() {
		if !a.Has("users") {
			continue
		}
		k := kb(
			row(cb("👋 Дать доступ гостя", "r:ux:"+id+":guest")),
			row(cb("👤 Дать доступ пользователя", "r:ux:"+id+":user")),
			row(cb("🚫 Игнорировать", "r:uxi:"+id)),
		)
		_, _ = b.tg.Send(ctx, a.ID, sb.String(), k)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func (b *Bot) redeem(ctx context.Context, from *TUser, code string) {
	u, inv, err := b.store.Redeem(code, from.ID, from.DisplayName(), from.Username)
	switch {
	case errors.Is(err, errInvite):
		_, _ = b.tg.Send(ctx, from.ID, "Ссылка-приглашение недействительна или устарела. Попросите новую.", nil)
		return
	case err != nil:
		log.Printf("redeem: %v", err)
		_, _ = b.tg.Send(ctx, from.ID, "Не получилось принять приглашение, попробуйте ещё раз.", nil)
		return
	}
	role := u.Role()
	b.audit.Add(u.ID, u.Name, "Принял приглашение ("+roleTitles[role]+")")
	_, _ = b.tg.Send(ctx, from.ID, "👋 Добро пожаловать! Вам открыт доступ к управлению "+esc(b.cfg.PCName)+".\nРоль: "+roleTitles[role], nil)
	text, k := b.home(u)
	b.newPanel(ctx, from.ID, text, k)
	if inv.By != 0 {
		_, _ = b.tg.Send(ctx, inv.By, "✅ "+esc(u.Name)+" принял приглашение. Роль: "+roleTitles[role], kb(row(cb("Открыть карточку", "r:u:"+itoa(u.ID)))))
	}
}

// Wake

func (b *Bot) editOrSend(ctx context.Context, chatID int64, msgID int, text string, k *KB) int {
	if msgID != 0 {
		if err := b.tg.Edit(ctx, chatID, msgID, text, k); err == nil {
			return msgID
		}
	}
	old := b.panels.Get(chatID)
	id, err := b.tg.Send(ctx, chatID, text, k)
	if err != nil {
		log.Printf("send: %v", err)
		return 0
	}
	b.panels.Set(chatID, id)
	if old != id {
		b.deleteQuiet(ctx, chatID, old)
	}
	return id
}

func (b *Bot) wake(ctx context.Context, user *User, chatID int64, msgID int, cbID string) {
	answer := func(text string, alert bool) {
		if cbID != "" {
			_ = b.tg.Answer(ctx, cbID, text, alert)
		}
	}
	if !user.Has("power_on") {
		answer("Нет прав на включение", true)
		return
	}
	if b.link.Online() {
		answer(b.cfg.PCName+" уже включён", false)
		text, k := b.home(user)
		b.editOrSend(ctx, chatID, msgID, text, k)
		return
	}
	sent, err := wake(ctx, b.cfg)
	if err != nil {
		log.Printf("wake: %v", err)
	}
	if sent == 0 {
		answer("Не получилось отправить сигнал", true)
		return
	}
	answer("Сигнал отправлен", false)
	b.audit.Add(user.ID, user.Name, "Включение ПК")
	text := "⏳ <b>Включаю " + esc(b.cfg.PCName) + "</b>\nСигнал отправлен, жду, когда ПК выйдет на связь..."
	id := b.editOrSend(ctx, chatID, msgID, text, kb(
		row(cb("🔁 Отправить сигнал ещё раз", "r:on")),
		row(cb("⬅️ Назад", "r:home")),
	))
	b.mu.Lock()
	if w, ok := b.wakes[chatID]; ok {
		w.msgID = id
	} else {
		b.wakes[chatID] = &wakeWait{chatID: chatID, msgID: id, start: b.now()}
	}
	b.mu.Unlock()
	go resendWake(ctx, b.cfg, b.link.Online)
}

func (b *Bot) wakeWatch(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := b.now()
		var expired []*wakeWait
		b.mu.Lock()
		for chatID, w := range b.wakes {
			if now.Sub(w.start) > wakeTimeout {
				expired = append(expired, w)
				delete(b.wakes, chatID)
			}
		}
		b.mu.Unlock()
		for _, w := range expired {
			text := "⚠️ <b>" + esc(b.cfg.PCName) + " не вышел на связь за 3 минуты</b>\n\nВозможно, ПК обесточен, в BIOS выключено включение по сети, или Windows загрузилась, но служба PcPult не запустилась."
			_ = b.tg.Edit(ctx, w.chatID, w.msgID, text, kb(row(cb("🔁 Попробовать ещё раз", "r:on")), row(cb("⬅️ Назад", "r:home"))))
		}
	}
}

// Link events

func (b *Bot) linkEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-b.link.events:
			b.onLinkEvent(ctx, ev)
		}
	}
}

func (b *Bot) onLinkEvent(ctx context.Context, ev LinkEvent) {
	pc := esc(b.cfg.PCName)
	now := b.now()
	skip := map[int64]bool{}
	powerKB := func(u User) *KB {
		if u.Has("power_on") {
			return kb(row(cb("⚡ Включить", "r:on")))
		}
		return nil
	}
	switch ev.Kind {
	case "online":
		b.clearNotices(ctx)
		b.mu.Lock()
		wakes := b.wakes
		b.wakes = map[int64]*wakeWait{}
		b.mu.Unlock()
		for _, w := range wakes {
			skip[w.chatID] = true
			text := fmt.Sprintf("🟢 <b>%s включился</b> за %s", pc, humanDur(now.Sub(w.start)))
			_ = b.tg.Edit(ctx, w.chatID, w.msgID, text, kb(row(cb("📋 Открыть меню", "menu"))))
		}
		var text string
		switch {
		case ev.Fresh:
			text = "🟢 <b>" + pc + " включился</b>"
		case ev.Lost:
			text = "🟢 <b>" + pc + " снова на связи</b>"
		default:
			return
		}
		log.Printf("pc online (fresh=%v lost=%v)", ev.Fresh, ev.Lost)
		b.notify(ctx, skip, text, noticeOnlineTTL, func(User) *KB { return kb(row(cb("📋 Меню", "menu"))) })
	case "bye":
		log.Printf("pc bye: %s", ev.Reason)
		texts := map[string]string{
			"shutdown":  "🔴 <b>" + pc + " выключается</b>",
			"restart":   "🔄 <b>" + pc + " перезагружается</b>",
			"sleep":     "😴 <b>" + pc + " уходит в сон</b>",
			"hibernate": "😴 <b>" + pc + " уходит в гибернацию</b>",
			"stop":      "⏹ <b>Служба PcPult на " + pc + " остановлена</b>",
		}
		text, ok := texts[ev.Reason]
		if !ok {
			text = "🔴 <b>" + pc + " отключился</b>"
		}
		if ev.By != 0 {
			skip[ev.By] = true
		}
		b.notify(ctx, skip, text, 0, powerKB)
	case "lost":
		log.Printf("pc lost")
		b.notify(ctx, skip, "⚠️ <b>"+pc+" пропал со связи</b>\nПК не отвечает больше 45 секунд: возможно, завис, пропала сеть или питание.", 0, powerKB)
	}
}

// Notices

const noticeOnlineTTL = 15 * time.Minute

func (b *Bot) notify(ctx context.Context, skip map[int64]bool, text string, ttl time.Duration, kbf func(User) *KB) {
	for _, u := range b.store.Recipients("status") {
		if skip[u.ID] {
			continue
		}
		id, err := b.tg.Send(ctx, u.ID, text, kbf(u))
		if err != nil {
			log.Printf("notify %d: %v", u.ID, err)
			continue
		}
		b.mu.Lock()
		old := b.notices[u.ID]
		b.notices[u.ID] = id
		b.mu.Unlock()
		b.deleteQuiet(ctx, u.ID, old)
		if ttl > 0 {
			go b.expireNotice(ctx, u.ID, id, ttl)
		}
	}
}

func (b *Bot) expireNotice(ctx context.Context, chatID int64, msgID int, ttl time.Duration) {
	sleepCtx(ctx, ttl)
	if ctx.Err() != nil {
		return
	}
	b.mu.Lock()
	current := b.notices[chatID] == msgID
	if current {
		delete(b.notices, chatID)
	}
	b.mu.Unlock()
	if current {
		b.deleteQuiet(ctx, chatID, msgID)
	}
}

func (b *Bot) clearNotices(ctx context.Context) {
	b.mu.Lock()
	old := b.notices
	b.notices = map[int64]int{}
	b.mu.Unlock()
	for chatID, id := range old {
		b.deleteQuiet(ctx, chatID, id)
	}
}
