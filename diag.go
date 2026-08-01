package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fortochka/core"
)

// Диагностика через локальный контроллер sing-box (clash_api): какой канал
// активен, какие отвечают, какие душатся. Всё по loopback, наружу не торчит.

// noProxyTransport — ОДИН общий транспорт без системного прокси. Переиспользуется
// во всех клиентах (пробник, опрос каналов, логи), иначе новый Transport на каждый
// вызов течёт соединениями/горутинами до «Not enough memory resources».
var noProxyTransport = &http.Transport{
	Proxy:               nil,
	MaxIdleConns:        20,
	MaxIdleConnsPerHost: 4,
	IdleConnTimeout:     30 * time.Second,
}

// directClient — HTTP-клиент БЕЗ системного прокси (общий пул соединений).
func directClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: noProxyTransport}
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
//
// Спрашиваем селектор `proxy`, а не группу `auto`: приложение при провале проверки
// объёмом закрепляет конкретный канал именно на `proxy`, и если смотреть в `auto`,
// журнал будет показывать выбор urltest, а трафик пойдёт другим путём. Пока
// закрепления не было, `proxy` указывает на `auto` — тогда разворачиваем до
// фактического канала внутри группы.
func (a *App) activeChannel() string {
	m, _ := a.clashJSON("/proxies/proxy")
	s, _ := m["now"].(string)
	if s == "" {
		return ""
	}
	if s == "auto" {
		inner, _ := a.clashJSON("/proxies/auto")
		if n, ok := inner["now"].(string); ok && n != "" {
			return n
		}
	}
	return s
}

func osArch() string { return runtime.GOOS + "/" + runtime.GOARCH }

// osDetail — версия ОС и число ядер: по журналу должно быть видно, на чём это крутилось.
func osDetail() string {
	host, _ := os.Hostname()
	if host != "" && len(host) > 24 {
		host = host[:24]
	}
	return fmt.Sprintf("go%s · %d CPU · хост %s", strings.TrimPrefix(runtime.Version(), "go"),
		runtime.NumCPU(), nonEmptyStr(host, "?"))
}

// logNetEnv — состояние сети ДО подключения: системный прокси и наличие чужих
// туннелей. Обе вещи регулярно оказывались причиной «у меня не работает», и обе
// раньше в журнал не попадали.
func (a *App) logNetEnv() {
	// Значение переменной прокси в журнал НЕ пишем: в нём часто логин и пароль
	// (http://user:pass@host), а журнал уходит на сервер. Достаточно факта и хоста.
	var proxies []string
	for _, v := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		for _, name := range []string{v, strings.ToLower(v)} {
			s := os.Getenv(name)
			if s == "" {
				continue
			}
			host := s
			if u, err := url.Parse(s); err == nil && u.Host != "" {
				host = u.Host // без схемы и без учётных данных
			}
			proxies = append(proxies, name+"="+host)
		}
	}
	if len(proxies) > 0 {
		a.log("системный прокси в окружении: %s (диагностика ходит мимо него)", strings.Join(proxies, ", "))
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		a.log("сетевые интерфейсы: не прочитались — %v", err)
		return
	}
	var tuns []string
	for _, f := range ifaces {
		if f.Flags&net.FlagUp == 0 {
			continue
		}
		n := strings.ToLower(f.Name)
		if strings.Contains(n, "tun") || strings.Contains(n, "tap") ||
			strings.Contains(n, "wg") || strings.Contains(n, "wintun") {
			tuns = append(tuns, fmt.Sprintf("%s(mtu %d)", f.Name, f.MTU))
		}
	}
	if len(tuns) > 0 {
		a.log("уже подняты туннельные интерфейсы: %s — чужой VPN может перехватывать маршрут",
			strings.Join(tuns, ", "))
	}
}

// logConfigSummary — что именно ушло в движок: аутбаунды, авто-пул, DNS, MTU.
// Без этого при разборе жалобы приходилось гадать, какой конфиг был собран.
func (a *App) logConfigSummary(cfg []byte) {
	var m struct {
		Outbounds []struct {
			Type      string   `json:"type"`
			Tag       string   `json:"tag"`
			Server    string   `json:"server"`
			Port      int      `json:"server_port"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
		Inbounds []struct {
			MTU int `json:"mtu"`
		} `json:"inbounds"`
		DNS struct {
			Strategy string `json:"strategy"`
		} `json:"dns"`
	}
	if json.Unmarshal(cfg, &m) != nil {
		a.log("  конфиг: %d Б (разобрать для сводки не удалось)", len(cfg))
		return
	}
	var real int
	for _, o := range m.Outbounds {
		switch o.Type {
		case "selector", "urltest", "direct":
		default:
			real++
		}
		if o.Tag == "auto" {
			a.log("  авто-выбор из %d: %s", len(o.Outbounds), strings.Join(o.Outbounds, ", "))
		}
	}
	mtu := 0
	if len(m.Inbounds) > 0 {
		mtu = m.Inbounds[0].MTU
	}
	a.log("  аутбаундов %d · MTU %d · DNS %s · конфиг %d Б", real, mtu, m.DNS.Strategy, len(cfg))
}

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
	if sp.OnlyListed {
		var parts []string
		parts = append(parts, "режим: только выбранное через туннель")
		if len(sp.Apps) > 0 {
			parts = append(parts, fmt.Sprintf("%d прил. через туннель", len(sp.Apps)))
		}
		if len(sp.Sites) > 0 {
			parts = append(parts, fmt.Sprintf("%d сайтов через туннель", len(sp.Sites)))
		}
		if len(parts) == 1 {
			return parts[0] + ", список пуст"
		}
		return strings.Join(parts, ", ")
	}
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
	// Категории РФ-сервисов в журнал попадают отдельной строкой: когда человек
	// жалуется «не открывается Сбербанк», первым делом смотрят, был ли он в обходе.
	if n := len(sp.RuCats); n > 0 {
		part := fmt.Sprintf("РФ-сервисы напрямую: %s", strings.Join(sp.RuCats, ", "))
		if len(sp.RuOff) > 0 {
			part += fmt.Sprintf(" (снято доменов: %d)", len(sp.RuOff))
		}
		parts = append(parts, part)
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
		if why := core.Unsupported(p); why != "" {
			a.log("  ⊘ %s — %s:%d [%s] пропущен: %s", name, p.Server, p.Port, kind, why)
			continue
		}
		mark, note := "•", "  (в авто-выбор не берём: прямой TCP на IP; проверим объёмом)"
		if core.Resilient(p) {
			mark, note = "✓", ""
		}
		sni := p.SNI
		if sni == "" {
			sni = "—"
		}
		a.log("  %s %s — %s:%d [%s] sni=%s%s", mark, name, p.Server, p.Port, kind, sni, note)
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

// SelfTest — кнопка «Тест»: прогон проверки исправности для UI.
func (a *App) SelfTest() map[string]any {
	res := map[string]any{"connected": a.State() == "connected"}
	if a.State() != "connected" {
		res["ok"] = false
		res["message"] = "VPN не подключён — сначала нажмите «Подключиться»"
		return res
	}
	tunnel := a.probe(6 * time.Second)
	res["tunnel"] = tunnel
	res["active"] = a.activeChannel()
	var chans []map[string]any
	for _, tag := range a.autoChannels() {
		ms, ok := a.testChannel(tag)
		chans = append(chans, map[string]any{"tag": tag, "ms": ms, "ok": ok})
	}
	res["channels"] = chans
	res["exitIP"] = a.ExitIP()
	res["ok"] = tunnel
	if tunnel {
		res["message"] = "Всё работает — трафик идёт через туннель"
	} else {
		res["message"] = "Туннель не отвечает — смените сервер/протокол"
	}
	return res
}

// --- проверка канала объёмом ---
//
// Главный урок аварии: пробник generate_204 (несколько сотен байт) проходит и по
// каналу, который замерзает на реальном трафике. Из-за этого urltest сажал людей на
// мёртвый канал, а журнал показывал «подключено». Поэтому после подключения и раз в
// несколько минут скачиваем настоящий объём и смотрим, доходит ли он.

const (
	volumeBytes   = 512 << 10      // сколько тянем: заведомо больше порога заморозки (~15–20 КБ)
	volumeTimeout = 20 * time.Second
	volumeMinOK   = 128 << 10 // меньше этого за отведённое время — канал не держит объём
)

// volumeOK тянет крупный файл через туннель. Возвращает (держит ли, что видели).
func (a *App) volumeOK() (bool, string) {
	client := directClient(volumeTimeout)
	// Cloudflare отдаёт ровно столько байт, сколько попросили, и есть везде.
	url := fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", volumeBytes)
	started := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return false, fmt.Sprintf("запрос не прошёл: %v", err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, volumeBytes))
	el := time.Since(started)
	if n < volumeMinOK {
		reason := "оборвалось"
		if err == nil {
			reason = "не докачалось"
		}
		return false, fmt.Sprintf("%s на %d КБ из %d за %s (%v)",
			reason, n>>10, volumeBytes>>10, el.Round(time.Millisecond), err)
	}
	kbps := float64(n) / 1024 / el.Seconds()
	return true, fmt.Sprintf("%d КБ за %s, ~%.0f КБ/с", n>>10, el.Round(time.Millisecond), kbps)
}

// selectChannel переключает селектор proxy на конкретный канал через контроллер.
func (a *App) selectChannel(tag string) bool {
	a.mu.Lock()
	addr := a.clashAddr
	a.mu.Unlock()
	if addr == "" {
		return false
	}
	body := strings.NewReader(`{"name":` + strconv.Quote(tag) + `}`)
	req, err := http.NewRequest("PUT", "http://"+addr+"/proxies/proxy", body)
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := directClient(8 * time.Second).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode/100 == 2
}

// allChannels — все каналы селектора proxy, в порядке из конфига (auto первым).
func (a *App) allChannels() []string {
	m, ok := a.clashJSON("/proxies/proxy")
	if !ok {
		return nil
	}
	var out []string
	if all, ok := m["all"].([]any); ok {
		for _, t := range all {
			if s, ok := t.(string); ok && s != "auto" {
				out = append(out, s)
			}
		}
	}
	return out
}

// ensureVolume добивается канала, который реально держит трафик: проверяет текущий,
// а если тот не тянет — перебирает остальные и закрепляет первый рабочий.
//
// Именно здесь чинится главная слепота прежней версии: раньше прямые профили на IP
// датацентра были исключены из авто-выбора навсегда и списком, по теории о заморозке.
// Теория проверялась одним наблюдением и объясняла не всё, а цена ошибки — юзер сидит
// без единого рабочего канала, хотя один работает. Теперь решает измерение: любой
// канал допускается, но только пока проходит объём.
func (a *App) ensureVolume(myGen uint64) {
	if ok, note := a.volumeOK(); ok {
		a.log("  канал держит объём: %s", note)
		return
	} else {
		a.log("  текущий канал не держит объём: %s", note)
	}
	cands := a.allChannels()
	if len(cands) == 0 {
		a.log("  перебрать нечего — контроллер не отдал список каналов")
		return
	}
	active := a.activeChannel()
	for _, tag := range cands {
		if a.superseded(myGen) || a.State() == "disconnected" {
			return
		}
		if tag == active {
			continue // его только что проверили
		}
		if !a.selectChannel(tag) {
			a.log("  ✗ %s — не удалось переключиться", tag)
			continue
		}
		time.Sleep(1200 * time.Millisecond) // дать соединению встать
		ok, note := a.volumeOK()
		if ok {
			a.log("  ✓ %s — держит объём: %s → закрепляю этот канал", tag, note)
			a.mu.Lock()
			a.pinned = tag
			a.lastChan = tag
			a.mu.Unlock()
			return
		}
		a.log("  ✗ %s — %s", tag, note)
	}
	a.log("  ни один канал не держит объём — трафик пойдёт, но будет виснуть; "+
		"это сообщение нужно показать администратору")
	a.pushLog()
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
