package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// Шапка: версия, платформа, сессия, состояние. Раньше на сервер уезжал голый
	// хвост журнала, и если он обрезался — было не понять даже, чей это клиент.
	a.mu.Lock()
	sess, state, pinned := a.sess, a.state, a.pinned
	a.mu.Unlock()
	head := fmt.Sprintf("### Форточка %s · %s · сессия %s · устройство %s · состояние %s · отправлено %s\n",
		version, osArch(), nonEmptyStr(sess, "—"), deviceID(), state,
		time.Now().Format("2006-01-02 15:04:05-07:00"))
	if pinned != "" {
		head += fmt.Sprintf("### канал закреплён приложением: %s (авто-выбор не держал объём)\n", pinned)
	}
	body = append([]byte(head), body...)
	go a.deliver(subid, body)
}

// logEndpoints — куда пытаться отправить журнал, по порядку.
//
// Первым идёт origin: адрес за CDN (vpn.rungvard.net) из потребительских сетей РФ
// не отвечает вовсе — измерено 2026-07-26, TLS 0/8 при живом ICMP. Именно поэтому
// за всё время до сервера доехал ровно один журнал: отправка молча падала у всех,
// ошибка в коде игнорировалась. CDN оставлен запасным на случай, когда наоборот
// заблокируют домен origin.
var logEndpoints = []string{
	"https://rungvard.net/log/",
	"https://vpn.rungvard.net/log/",
}

// pendingDir — журналы, которые не удалось отправить. Лежат до следующей попытки:
// потерять отчёт о падении связи из-за того, что в этот момент не было связи, —
// худший из возможных сценариев для диагностики.
func pendingDir() string {
	d := filepath.Join(configDir(), "pending-logs")
	os.MkdirAll(d, 0o700)
	return d
}

// deliverMu — отправка журналов идёт из нескольких мест (завершение попытки, падение
// связи, реконнект, отключение) и запускается горутинами. Без замка два вызова
// одновременно разгребают очередь: один читает файл, другой его уже удалил, — и на
// сервер уходят дубли или, наоборот, запись теряется.
var deliverMu sync.Mutex

func (a *App) deliver(subid string, body []byte) {
	deliverMu.Lock()
	defer deliverMu.Unlock()
	if a.post(subid, body) {
		a.flushPending()
		return
	}
	// Не ушло — складываем в очередь. Имя содержит subId и время, чтобы на сервере
	// потом было понятно, чьё это и когда случилось.
	name := fmt.Sprintf("%s__%s.log", strings.ReplaceAll(subid, "/", "_"),
		time.Now().UTC().Format("20060102T150405"))
	if err := os.WriteFile(filepath.Join(pendingDir(), name), body, 0o600); err == nil {
		a.log("журнал не отправлен, отложен в очередь (%s)", name)
	}
}

// flushPending досылает всё, что накопилось, когда связь появилась.
func (a *App) flushPending() {
	ents, err := os.ReadDir(pendingDir())
	if err != nil {
		return
	}
	sent := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		p := filepath.Join(pendingDir(), e.Name())
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			os.Remove(p)
			continue
		}
		sub := strings.SplitN(e.Name(), "__", 2)[0]
		if !a.post(sub, b) {
			break // связь снова пропала — остальные подождут
		}
		os.Remove(p)
		sent++
	}
	if sent > 0 {
		a.log("досланы отложенные журналы: %d", sent)
	}
}

// post пробует адреса по очереди. Возвращает, ушло ли.
func (a *App) post(subid string, body []byte) bool {
	var last string
	for _, base := range logEndpoints {
		req, err := http.NewRequest("POST", base+url.PathEscape(subid), bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp, err := directClient(10 * time.Second).Do(req) // без системного прокси
		if err != nil {
			last = err.Error()
			continue
		}
		code := resp.StatusCode
		resp.Body.Close()
		if code/100 == 2 {
			return true
		}
		last = fmt.Sprintf("%s → HTTP %d", base, code)
	}
	if last != "" {
		a.log("отправка журнала не удалась: %s", last)
	}
	return false
}
