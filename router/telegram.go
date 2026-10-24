package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Types

type TUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

func (u TUser) DisplayName() string {
	n := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if n == "" {
		n = u.Username
	}
	if n == "" {
		n = fmt.Sprintf("id %d", u.ID)
	}
	return n
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type ForwardOrigin struct {
	Type       string `json:"type"`
	SenderUser *TUser `json:"sender_user"`
}

type Message struct {
	MessageID     int            `json:"message_id"`
	From          *TUser         `json:"from"`
	Chat          Chat           `json:"chat"`
	Text          string         `json:"text"`
	ForwardOrigin *ForwardOrigin `json:"forward_origin"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    TUser    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`

	Raw json.RawMessage `json:"-"`
}

type Btn struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

type KB struct {
	Rows [][]Btn `json:"inline_keyboard"`
}

func kb(rows ...[]Btn) *KB {
	k := &KB{Rows: [][]Btn{}}
	for _, r := range rows {
		if len(r) > 0 {
			k.Rows = append(k.Rows, r)
		}
	}
	return k
}

func row(btns ...Btn) []Btn { return btns }

func cb(text, data string) Btn { return Btn{Text: text, Data: data} }

// Client

type TG struct {
	base  string
	token string
	http  *http.Client
}

const badResponse = "bad response"

type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

func newTransport(proxy string) (*http.Transport, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 8
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return tr, nil
}

func newTG(base, token string, tr http.RoundTripper) *TG {
	return &TG{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Transport: tr, Timeout: 90 * time.Second}}
}

func (t *TG) redact(s string) string {
	if t.token == "" {
		return s
	}
	return strings.ReplaceAll(s, t.token, "<token>")
}

func (t *TG) Call(ctx context.Context, method string, params any, out any) error {
	for attempt := 0; ; attempt++ {
		err := t.call(ctx, method, params, out)
		var ae *APIError
		if errors.As(err, &ae) && ae.Code == 429 && attempt < 2 {
			wait := time.Duration(min(max(ae.RetryAfter, 1), 30)) * time.Second
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
}

func (t *TG) call(ctx context.Context, method string, params any, out any) error {
	body := []byte("{}")
	if params != nil {
		var err error
		if body, err = json.Marshal(params); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New(t.redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return errors.New(t.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return &APIError{Code: resp.StatusCode, Description: badResponse + ": " + method}
	}
	if !r.OK {
		return &APIError{Code: r.ErrorCode, Description: r.Description, RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (t *TG) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var raws []json.RawMessage
	err := t.Call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}, &raws)
	if err != nil {
		return nil, err
	}
	ups := make([]Update, 0, len(raws))
	for _, r := range raws {
		var u Update
		if err := json.Unmarshal(r, &u); err != nil {
			continue
		}
		u.Raw = r
		ups = append(ups, u)
	}
	return ups, nil
}

// Helpers

func (t *TG) Send(ctx context.Context, chatID int64, text string, k *KB) (int, error) {
	p := map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if k != nil {
		p["reply_markup"] = k
	}
	var m Message
	err := t.Call(ctx, "sendMessage", p, &m)
	return m.MessageID, err
}

func (t *TG) Edit(ctx context.Context, chatID int64, msgID int, text string, k *KB) error {
	p := map[string]any{"chat_id": chatID, "message_id": msgID, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if k != nil {
		p["reply_markup"] = k
	}
	err := t.Call(ctx, "editMessageText", p, nil)
	var ae *APIError
	if errors.As(err, &ae) && strings.Contains(ae.Description, "message is not modified") {
		return nil
	}
	return err
}

func (t *TG) Answer(ctx context.Context, cbID, text string, alert bool) error {
	p := map[string]any{"callback_query_id": cbID}
	if text != "" {
		p["text"] = text
		p["show_alert"] = alert
	}
	return t.Call(ctx, "answerCallbackQuery", p, nil)
}

func (t *TG) Delete(ctx context.Context, chatID int64, msgID int) error {
	return t.Call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": msgID}, nil)
}
