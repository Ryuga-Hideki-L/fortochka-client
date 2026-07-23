package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// deviceID — стабильный случайный ID машины (для логов прямых вставок без подписки).
func deviceID() string {
	p := filepath.Join(configDir(), "fortochka.id")
	if b, err := os.ReadFile(p); err == nil {
		if s := strings.TrimSpace(string(b)); len(s) >= 6 {
			return s
		}
	}
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	id := hex.EncodeToString(buf)
	os.WriteFile(p, []byte(id), 0o600)
	return id
}

// subIDFromLink вытаскивает subId из ссылки-подписки (…/sub/<subid>).
// Пусто — если это прямая vless-вставка без подписки (тогда лог не шлём).
func subIDFromLink(link string) string {
	i := strings.Index(link, "/sub/")
	if i < 0 {
		return ""
	}
	s := link[i+len("/sub/"):]
	if j := strings.IndexAny(s, "?#/"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// pushLog — отправить текущий журнал на сервер (subId берём из сохранённой ссылки).
// Зовём в ключевых точках: попытка, падение связи, реконнект, отключение.
func (a *App) pushLog() {
	a.mu.Lock()
	link := a.link
	a.mu.Unlock()
	a.sendLogToServer(link)
}

// sendLogToServer шлёт журнал попытки подключения на сервер под subId юзера,
// чтобы админ видел его в панели без ручной пересылки. Тихо, не блокирует.
func (a *App) sendLogToServer(link string) {
	subid := subIDFromLink(link)
	if subid == "" {
		subid = "dev-" + deviceID() // прямая вставка без подписки — шлём под ID устройства
	}
	body, err := os.ReadFile(logPath())
	if err != nil || len(body) == 0 {
		return
	}
	if len(body) > 64*1024 { // шлём хвост, если журнал большой
		body = body[len(body)-64*1024:]
	}
	go func() {
		u := "https://vpn.rungvard.net/log/" + url.PathEscape(subid)
		req, err := http.NewRequest("POST", u, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp, err := directClient(10 * time.Second).Do(req) // без системного прокси
		if err == nil {
			resp.Body.Close()
		}
	}()
}
