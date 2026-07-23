package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"fortochka/core"
)

// Диагностика через локальный контроллер sing-box (clash_api): какой канал
// активен, какие отвечают, какие душатся. Всё по loopback, наружу не торчит.

// directClient — HTTP-клиент БЕЗ системного прокси. Запросы к 127.0.0.1 (контроллер)
// не должны уходить через прокси юзера — иначе диагностика молчит.
func directClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}}
}

// freeLoopbackAddr — свободный порт на 127.0.0.1 для контроллера. "" если не вышло.
func freeLoopbackAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ""
	}
	addr := l.Addr().String()
	l.Close() // sing-box займёт этот порт через доли секунды
	return addr
}

func (a *App) clashJSON(path string) (map[string]any, bool) {
	a.mu.Lock()
	addr := a.clashAddr
	a.mu.Unlock()
	if addr == "" {
		return nil, false
	}
	c := directClient(8 * time.Second)
	resp, err := c.Get("http://" + addr + path)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	var m map[string]any
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return nil, false
	}
	return m, resp.StatusCode == 200
}

// autoChannels — каналы в группе авто-выбора (в порядке urltest).
func (a *App) autoChannels() []string {
	m, ok := a.clashJSON("/proxies/auto")
	if !ok {
		return nil
	}
	var out []string
	if all, ok := m["all"].([]any); ok {
		for _, t := range all {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// testChannel форсирует проверку канала: (задержка_мс, ответил ли).
func (a *App) testChannel(tag string) (int, bool) {
	a.mu.Lock()
	addr := a.clashAddr
	a.mu.Unlock()
	if addr == "" {
		return 0, false
	}
	c := directClient(9 * time.Second)
	u := "http://" + addr + "/proxies/" + url.PathEscape(tag) + "/delay?url=" +
		url.QueryEscape("https://www.gstatic.com/generate_204") + "&timeout=5000"
	resp, err := c.Get(u)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, false
	}
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	if d, ok := m["delay"].(float64); ok {
		return int(d), true
	}
	return 0, false
}

// activeChannel — какой канал сейчас реально выбран.
func (a *App) activeChannel() string {
	m, _ := a.clashJSON("/proxies/auto")
	if s, ok := m["now"].(string); ok {
		return s
	}
	return ""
}

func osArch() string { return runtime.GOOS + "/" + runtime.GOARCH }

// sourceDesc — что за ссылку вставил юзер (без выдачи subId целиком).
func sourceDesc(link string) string {
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		host := link
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		return "подписка (" + host + ")"
	}
	var kinds []string
	seen := map[string]bool{}
	for _, k := range []struct{ p, n string }{
		{"vless://", "vless"}, {"hysteria2://", "hysteria2"}, {"hy2://", "hysteria2"}, {"tuic://", "tuic"},
	} {
		if strings.Contains(link, k.p) && !seen[k.n] {
			seen[k.n] = true
			kinds = append(kinds, k.n)
		}
	}
	if len(kinds) == 0 {
		return "прямая ссылка"
	}
	return "прямые ссылки (" + strings.Join(kinds, ", ") + ")"
}

// splitDesc — краткое описание раздельного туннелирования.
func splitDesc(sp core.Split) string {
	var parts []string
	if sp.BypassRu {
		parts = append(parts, "РФ-сайты напрямую")
	}
	if len(sp.Apps) > 0 {
		parts = append(parts, fmt.Sprintf("%d прил. мимо туннеля", len(sp.Apps)))
	}
	if len(sp.Sites) > 0 {
		parts = append(parts, fmt.Sprintf("%d сайтов мимо туннеля", len(sp.Sites)))
	}
	if len(parts) == 0 {
		return "весь трафик через туннель"
	}
	return strings.Join(parts, ", ")
}

// logProfiles — сводка загруженных профилей без секретов (сервер:порт/протокол).
// ✓ = участвует в авто-выборе (устойчив к ТСПУ), • = только вручную.
func (a *App) logProfiles(profiles []core.Profile) {
	for i, p := range profiles {
		proto := p.Proto
		if proto == "" {
			proto = "vless"
		}
		kind := proto
		if proto == "vless" {
			kind = "vless/" + p.Net
			if p.Sec == "reality" {
				kind += "+reality"
			}
		}
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("сервер #%d", i+1)
		}
		mark, note := "•", "  (вручную — душится ТСПУ)"
		if core.Resilient(p) {
			mark, note = "✓", ""
		}
		a.log("  %s %s — %s:%d [%s]%s", mark, name, p.Server, p.Port, kind, note)
	}
}

// logChannels прогоняет все каналы авто-выбора и пишет попытки + активный.
func (a *App) logChannels() {
	chans := a.autoChannels()
	if len(chans) == 0 {
		a.log("диагностика каналов недоступна (контроллер не ответил)")
		return
	}
	a.log("подбираю рабочий канал (проверок: %d)…", len(chans))
	for _, tag := range chans {
		if ms, ok := a.testChannel(tag); ok {
			a.log("  ✓ %s — отвечает, %d мс", tag, ms)
		} else {
			a.log("  ✗ %s — молчит (душится или недоступен)", tag)
		}
	}
	if now := a.activeChannel(); now != "" {
		a.log("→ активный канал: %s", now)
		a.mu.Lock()
		a.lastChan = now
		a.mu.Unlock()
	}
}

// noteChannelSwitch логирует смену активного канала (failover).
func (a *App) noteChannelSwitch() {
	now := a.activeChannel()
	if now == "" {
		return
	}
	a.mu.Lock()
	prev := a.lastChan
	a.lastChan = now
	a.mu.Unlock()
	if prev != "" && prev != now {
		a.log("переключение канала: %s → %s", prev, now)
	}
}
