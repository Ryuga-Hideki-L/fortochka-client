package main

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

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

// sendLogToServer шлёт журнал попытки подключения на сервер под subId юзера,
// чтобы админ видел его в панели без ручной пересылки. Тихо, не блокирует.
func (a *App) sendLogToServer(link string) {
	subid := subIDFromLink(link)
	if subid == "" {
		return
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
