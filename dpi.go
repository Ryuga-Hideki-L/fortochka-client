package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// «Запрет» — локальный обход DPI без туннеля (расшить замедление YouTube/Discord
// и заблокированные домены прямо на машине). Два движка:
//   - ByeDPI  — user-space SOCKS5, БЕЗ драйвера, уживается с VPN (можно вместе).
//   - Zapret  — winws на WinDivert, мощнее, но нужен драйвер/админ и лучше ВМЕСТО VPN.

const byedpiPort = "1081" // SOCKS byedpi слушает тут; приложение ставит системный прокси

// DPIPreset — стратегия/пресет обхода для UI.
type DPIPreset struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	args []string `json:"-"`
}

var byedpiPresets = []DPIPreset{
	{ID: "auto", Name: "Авто (универсально)", args: []string{"--auto=torst", "--tlsrec=1+s"}},
	{ID: "youtube", Name: "YouTube / видео", args: []string{"--split=1", "--disorder", "--tlsrec=1+s"}},
	{ID: "discord", Name: "Discord", args: []string{"--fake=1", "--split=2", "--disorder"}},
	{ID: "strong", Name: "Сильный (если не помогает)", args: []string{"--auto=torst", "--fake=1", "--tlsrec=1+s", "--disorder"}},
}

var zapretPresets = []DPIPreset{
	{ID: "general", Name: "Универсальный", args: []string{"--wf-tcp=80,443", "--dpi-desync=fake,split2", "--dpi-desync-ttl=13"}},
	{ID: "youtube", Name: "YouTube / видео", args: []string{"--wf-tcp=443", "--dpi-desync=split", "--dpi-desync-split-pos=1460", "--dpi-desync-ttl=13"}},
	{ID: "discord", Name: "Discord", args: []string{"--wf-tcp=443", "--wf-udp=443,19294-19344", "--dpi-desync=fake,split2", "--dpi-desync-ttl=13"}},
}

type dpiEngine struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	logFile *os.File
	engine  string
	preset  string
	running bool
}

var dpi = &dpiEngine{}

// DPIPresets — список пресетов для выбранного движка (для UI).
func (a *App) DPIPresets(engine string) []DPIPreset {
	if engine == "zapret" {
		return zapretPresets
	}
	return byedpiPresets
}

// DPIStatus — текущее состояние для UI.
func (a *App) DPIStatus() map[string]any {
	dpi.mu.Lock()
	defer dpi.mu.Unlock()
	return map[string]any{"running": dpi.running, "engine": dpi.engine, "preset": dpi.preset}
}

func dpiBin(engine string) string {
	name := "byedpi.exe"
	if engine == "zapret" {
		name = "winws.exe"
	}
	if runtime.GOOS != "windows" {
		name = "byedpi"
		if engine == "zapret" {
			name = "nfqws"
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, c := range []string{filepath.Join(dir, name), filepath.Join(dir, "dpi", name)} {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return ""
}

func presetArgs(engine, id string) []string {
	list := byedpiPresets
	if engine == "zapret" {
		list = zapretPresets
	}
	for _, p := range list {
		if p.ID == id {
			return p.args
		}
	}
	if len(list) > 0 {
		return list[0].args
	}
	return nil
}

// DPIStart запускает выбранный движок с пресетом. Возвращает "" при успехе или текст ошибки.
func (a *App) DPIStart(engine, preset string) string {
	if engine != "zapret" {
		engine = "byedpi"
	}
	// Zapret (WinDivert) конфликтует с TUN-туннелем — не даём запускать при активном/поднимающемся VPN.
	if engine == "zapret" && a.State() != "disconnected" {
		return "Zapret нельзя вместе с VPN — сначала отключите VPN (или выберите ByeDPI)"
	}
	bin := dpiBin(engine)
	if bin == "" {
		return "Движок не найден в комплекте (нужна сборка с бинарями)"
	}
	dpi.mu.Lock()
	defer dpi.mu.Unlock()
	if dpi.running {
		a.dpiStopLocked()
	}
	var args []string
	if engine == "byedpi" {
		args = append([]string{"-i", "127.0.0.1", "-p", byedpiPort}, presetArgs(engine, preset)...)
	} else {
		args = presetArgs(engine, preset)
	}
	cmd := exec.Command(bin, args...)
	hideWindow(cmd)
	var logf *os.File
	if f, err := os.OpenFile(dpiLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		cmd.Stdout, cmd.Stderr = f, f
		logf = f
	}
	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		return "Не удалось запустить: " + err.Error()
	}
	dpi.cmd, dpi.logFile, dpi.engine, dpi.preset, dpi.running = cmd, logf, engine, preset, true
	if engine == "byedpi" {
		if err := setSysProxy("127.0.0.1:" + byedpiPort); err != nil {
			a.log("Запрет: не смог выставить системный прокси: %v", err)
		}
	}
	a.log("Запрет включён: %s / %s", engine, preset)
	return ""
}

// DPIStop останавливает движок.
func (a *App) DPIStop() {
	dpi.mu.Lock()
	defer dpi.mu.Unlock()
	a.dpiStopLocked()
	a.log("Запрет выключен")
}

func (a *App) dpiStopLocked() {
	if dpi.engine == "byedpi" {
		clearSysProxy()
	}
	if dpi.cmd != nil && dpi.cmd.Process != nil {
		dpi.cmd.Process.Kill()
		dpi.cmd.Wait()
	}
	if dpi.logFile != nil {
		dpi.logFile.Close()
	}
	dpi.cmd, dpi.logFile, dpi.engine, dpi.preset, dpi.running = nil, nil, "", "", false
}

func dpiLogPath() string { return filepath.Join(os.TempDir(), "fortochka-zapret.log") }

// Cleanup — кнопка «Завершить зависшие процессы»: корректно останавливает наш
// движок и «Запрет», снимает системный прокси и добивает зависшие
// sing-box/winws/byedpi по имени (на случай зомби после сбоя/нехватки ресурсов).
func (a *App) Cleanup() string {
	a.Disconnect()
	a.DPIStop()
	killStuckProcs()
	a.log("сброс: зависшие процессы завершены")
	return "Готово — зависшие процессы завершены"
}

// DPILog — последние строки лога движка (для UI/диагностики).
func (a *App) DPILog() string {
	b, err := os.ReadFile(dpiLogPath())
	if err != nil {
		return ""
	}
	return string(b)
}
