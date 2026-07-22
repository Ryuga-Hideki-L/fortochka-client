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
	ctx    context.Context
	engine *core.Engine
	mu     sync.Mutex
	link   string
	state  string
}

type settings struct {
	Link string `json:"link"`
}

func NewApp() *App {
	return &App{engine: core.NewEngine(), state: "disconnected"}
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
	}
}

func (a *App) saveSettings() {
	b, _ := json.MarshalIndent(settings{Link: a.link}, "", "  ")
	os.WriteFile(filepath.Join(configDir(), "settings.json"), b, 0o600)
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
	a.mu.Unlock()
	if link == "" {
		return "Вставьте ссылку подписки"
	}
	// новый журнал на сессию
	os.WriteFile(logPath(), []byte(fmt.Sprintf("%s  === подключение ===\n", time.Now().Format("15:04:05"))), 0o600)
	a.setState("connecting")

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
	cfg, err := core.BuildConfig(profiles)
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
	a.log("движок запущен")
	a.setState("connected")
	return ""
}

func (a *App) Disconnect() {
	a.engine.Stop()
	a.log("отключено")
	a.setState("disconnected")
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
