package main

import (
	"context"
	"encoding/json"
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

func (a *App) Connect() string {
	a.mu.Lock()
	link := a.link
	a.mu.Unlock()
	if link == "" {
		return "Вставьте ссылку подписки"
	}
	a.setState("connecting")

	profiles, err := core.FetchProfiles(link)
	if err != nil {
		a.setState("disconnected")
		return err.Error()
	}
	if len(profiles) == 0 {
		a.setState("disconnected")
		return "В подписке нет поддерживаемых профилей"
	}
	cfg, err := core.BuildConfig(profiles)
	if err != nil {
		a.setState("disconnected")
		return err.Error()
	}
	if err := a.engine.Start(cfg); err != nil {
		a.setState("disconnected")
		return err.Error()
	}
	a.setState("connected")
	return ""
}

func (a *App) Disconnect() {
	a.engine.Stop()
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
