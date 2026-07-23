package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fortochka/core"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx         context.Context
	engine      *core.Engine
	mu          sync.Mutex
	link        string
	state       string
	cfg         []byte        // последний рабочий конфиг — для авто-реконнекта
	healthStop  chan struct{} // закрытие останавливает health-loop
	bypassRu    bool          // РФ-домены напрямую
	bypassApps  []string      // приложения мимо туннеля
	bypassSites []string      // сайты мимо туннеля
	lastChan    string        // последний активный канал (для лога переключений)
	updNote     string        // статус обновления с последней проверки (для лога)
}

type settings struct {
	Link        string   `json:"link"`
	BypassRu    *bool    `json:"bypassRu,omitempty"` // указатель: nil = первый запуск → дефолт true
	BypassApps  []string `json:"bypassApps,omitempty"`
	BypassSites []string `json:"bypassSites,omitempty"`
}

// SplitCfg отдаётся во фронт для окна настроек.
type SplitCfg struct {
	BypassRu    bool     `json:"bypassRu"`
	BypassApps  []string `json:"bypassApps"`
	BypassSites []string `json:"bypassSites"`
}

func NewApp() *App {
	return &App{engine: core.NewEngine(), state: "disconnected", bypassRu: true}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadSettings()
}

func (a *App) shutdown(ctx context.Context) {
	a.engine.Stop()
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "Fortochka")
	os.MkdirAll(dir, 0o700)
	return dir
}

func (a *App) loadSettings() {
	b, err := os.ReadFile(filepath.Join(configDir(), "settings.json"))
	if err != nil {
		return
	}
	var s settings
	if json.Unmarshal(b, &s) == nil {
		a.link = s.Link
		if s.BypassRu != nil {
			a.bypassRu = *s.BypassRu
		}
		a.bypassApps = s.BypassApps
		a.bypassSites = s.BypassSites
	}
}

func (a *App) saveSettings() {
	ru := a.bypassRu
	b, _ := json.MarshalIndent(settings{Link: a.link, BypassRu: &ru,
		BypassApps: a.bypassApps, BypassSites: a.bypassSites}, "", "  ")
	os.WriteFile(filepath.Join(configDir(), "settings.json"), b, 0o600)
}

// GetSplit / SetSplit — настройки раздельного туннелирования для окна настроек.
func (a *App) GetSplit() SplitCfg {
	a.mu.Lock()
	defer a.mu.Unlock()
	return SplitCfg{BypassRu: a.bypassRu, BypassApps: a.bypassApps, BypassSites: a.bypassSites}
}

func (a *App) SetSplit(bypassRu bool, apps []string, sites []string) {
	a.mu.Lock()
	a.bypassRu = bypassRu
	a.bypassApps = cleanList(apps)
	a.bypassSites = cleanList(sites)
	a.mu.Unlock()
	a.saveSettings()
}

func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) GetLink() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.link
}

func (a *App) SetLink(link string) {
	link = strings.TrimSpace(link)
	// принимаем и hiddify://import/<url>, и голый url подписки
	if i := strings.Index(link, "://import/"); i >= 0 {
		link = link[i+len("://import/"):]
		if h := strings.IndexByte(link, '#'); h >= 0 {
			link = link[:h]
		}
	}
	a.mu.Lock()
	a.link = link
	a.mu.Unlock()
	a.saveSettings()
}

func (a *App) State() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *App) setState(s string) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "state", s)
}

func logPath() string { return filepath.Join(configDir(), "fortochka.log") }

func (a *App) log(format string, args ...any) {
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// GetLogs отдаёт хвост журнала (свой + движка) для окна логов.
func (a *App) GetLogs() string {
	b, err := os.ReadFile(logPath())
	if err != nil {
		return "Журнал пуст."
	}
	const max = 16000
	if len(b) > max {
		b = b[len(b)-max:]
	}
	return string(b)
}

func (a *App) Connect() string {
	a.mu.Lock()
	link := a.link
	sp := core.Split{BypassRu: a.bypassRu, Apps: a.bypassApps, Sites: a.bypassSites}
	a.mu.Unlock()
	if link == "" {
		return "Вставьте ссылку подписки"
	}
	// новый журнал на сессию
	os.WriteFile(logPath(), []byte(fmt.Sprintf("%s  === подключение ===\n", time.Now().Format("15:04:05"))), 0o600)
	a.mu.Lock()
	a.lastChan = ""
	un := a.updNote
	a.mu.Unlock()
	a.log("Форточка %s · %s", version, osArch())
	if un != "" {
		a.log("%s", un)
	}
	a.log("источник: %s", sourceDesc(link))
	a.setState("connecting")

	a.log("скачиваю/разбираю ссылку…")
	profiles, err := core.FetchProfiles(link)
	if err != nil {
		a.log("подписка: %s", err)
		a.setState("disconnected")
		return err.Error()
	}
	a.log("получено профилей: %d", len(profiles))
	if len(profiles) == 0 {
		a.setState("disconnected")
		return "В подписке нет поддерживаемых профилей"
	}
	a.logProfiles(profiles)
	a.log("настройки: %s", splitDesc(sp))
	cfg, err := core.BuildConfig(profiles, sp)
	if err != nil {
		a.log("конфиг: %s", err)
		a.setState("disconnected")
		return err.Error()
	}
	if err := a.engine.Start(cfg, logPath()); err != nil {
		a.log("движок: %s", err)
		a.setState("disconnected")
		return err.Error()
	}
	a.log("движок запущен, проверяю выход в сеть…")

	// Не показываем «Подключено», пока реально не вышли через туннель.
	// Если туннель мёртв — strict_route блокирует трафик, проверка не пройдёт.
	if !a.probe(12 * time.Second) {
		a.log("туннель не поднялся — нет ответа через VPN")
		a.engine.Stop()
		a.setState("disconnected")
		return "Не удалось выйти в сеть через VPN. Проверьте ссылку или смените сервер."
	}
	a.log("туннель проверен, соединение активно")
	a.logChannels()

	a.mu.Lock()
	a.cfg = cfg
	stop := make(chan struct{})
	a.healthStop = stop
	a.mu.Unlock()
	go a.healthLoop(stop)

	a.setState("connected")
	return ""
}

func (a *App) Disconnect() {
	a.mu.Lock()
	if a.healthStop != nil {
		close(a.healthStop)
		a.healthStop = nil
	}
	a.mu.Unlock()
	a.engine.Stop()
	a.log("отключено")
	a.setState("disconnected")
}

// probe проверяет, что трафик реально уходит через туннель. Два хоста —
// чтобы блокировка/сбой одного не давал ложное «не подключено».
func (a *App) probe(within time.Duration) bool {
	client := &http.Client{Timeout: 4 * time.Second}
	urls := []string{"https://www.gstatic.com/generate_204", "https://cp.cloudflare.com/generate_204"}
	deadline := time.Now().Add(within)
	for {
		for _, u := range urls {
			resp, err := client.Get(u)
			if err == nil {
				code := resp.StatusCode
				resp.Body.Close()
				if code == 204 || code == 200 {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(700 * time.Millisecond)
	}
}

// healthLoop раз в 30с проверяет живость туннеля и переподключает при обрыве.
func (a *App) healthLoop(stop chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	fails := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if a.State() != "connected" {
				return
			}
			if a.probe(5 * time.Second) {
				fails = 0
				a.noteChannelSwitch() // залогировать, если urltest сменил канал
				continue
			}
			fails++
			a.log("проверка связи не прошла (%d/2)", fails)
			if fails >= 2 {
				a.reconnect(stop)
				fails = 0
			}
		}
	}
}

// reconnect перезапускает движок тем же конфигом. При неудаче трафик остаётся
// заблокированным kill-switch'ем (strict_route) — утечки нет.
func (a *App) reconnect(stop chan struct{}) {
	select {
	case <-stop: // уже отключились вручную
		return
	default:
	}
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if cfg == nil {
		return
	}
	a.log("обрыв — переподключаюсь…")
	a.engine.Stop()
	time.Sleep(1 * time.Second)
	if err := a.engine.Start(cfg, logPath()); err != nil {
		a.log("реконнект: движок не стартовал: %s", err)
		a.setState("disconnected")
		return
	}
	// пользователь мог нажать «Отключить», пока мы переподключались —
	// иначе останется живой sing-box после отключения (зомби-туннель).
	select {
	case <-stop:
		a.engine.Stop()
		return
	default:
	}
	if a.probe(12 * time.Second) {
		a.log("переподключено")
		a.logChannels()
	} else {
		a.log("реконнект не удался — трафик заблокирован (нет утечки)")
	}
}

// ExitIP запрашивается уже через туннель — показываем адрес выхода.
func (a *App) ExitIP() string {
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b))
}
