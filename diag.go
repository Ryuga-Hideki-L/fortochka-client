package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"fortochka/core"
)

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
	for _, k := range []struct{ p, n string }{
		{"vless://", "vless"}, {"hysteria2://", "hysteria2"}, {"hy2://", "hysteria2"}, {"tuic://", "tuic"},
	} {
		if strings.Contains(link, k.p) && !strings.Contains(strings.Join(kinds, " "), k.n) {
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

// Диагностика через локальный контроллер sing-box (clash_api): какой канал
// активен, какие отвечают, какие душатся. Всё по loopback, наружу не торчит.

const clashBase = "http://" + core.ClashAPIAddr

func clashJSON(path string) (map[string]any, bool) {
	c := &http.Client{Timeout: 8 * time.Second}
	resp, err := c.Get(clashBase + path)
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
func autoChannels() []string {
	m, ok := clashJSON("/proxies/auto")
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
func testChannel(tag string) (int, bool) {
	c := &http.Client{Timeout: 9 * time.Second}
	u := clashBase + "/proxies/" + url.PathEscape(tag) + "/delay?url=" +
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
func activeChannel() string {
	m, _ := clashJSON("/proxies/auto")
	if s, ok := m["now"].(string); ok {
		return s
	}
	return ""
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
	chans := autoChannels()
	if len(chans) == 0 {
		return
	}
	a.log("подбираю рабочий канал (проверок: %d)…", len(chans))
	for _, tag := range chans {
		if ms, ok := testChannel(tag); ok {
			a.log("  ✓ %s — отвечает, %d мс", tag, ms)
		} else {
			a.log("  ✗ %s — молчит (душится или недоступен)", tag)
		}
	}
	if now := activeChannel(); now != "" {
		a.log("→ активный канал: %s", now)
		a.mu.Lock()
		a.lastChan = now
		a.mu.Unlock()
	}
}

// noteChannelSwitch логирует смену активного канала (failover).
func (a *App) noteChannelSwitch() {
	now := activeChannel()
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
