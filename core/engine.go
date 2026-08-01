package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Engine управляет процессом sing-box.
type Engine struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	dir string
	bin string
	log *os.File
}

func NewEngine() *Engine { return &Engine{} }

func binaryPath() (string, error) {
	name := "sing-box"
	if runtime.GOOS == "windows" {
		name = "sing-box.exe"
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for _, c := range []string{filepath.Join(dir, name), filepath.Join(dir, "core", name)} {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", errors.New("движок sing-box не найден рядом с приложением")
}

func (e *Engine) Start(config []byte, logPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd != nil {
		return nil
	}
	bin, err := binaryPath()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "fortochka")
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, config, 0o600); err != nil {
		os.RemoveAll(dir)
		return err
	}
	// Ошибку открытия журнала раньше проглатывали: вывод движка просто некуда было
	// направить, и человек видел «пустые логи» без единого намёка на причину.
	// Теперь причина попадает в сам журнал приложения, а если и он недоступен —
	// в текст ошибки запуска. Молчать здесь нельзя: журнал — единственное, по чему
	// потом разбирают, почему не подключилось.
	logf, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if logErr != nil {
		Log("ВНИМАНИЕ: журнал движка не ведётся — не удалось открыть %s: %v", logPath, logErr)
	}

	cmd := exec.Command(bin, "run", "-c", cfgPath)
	cmd.Dir = dir
	if logf != nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		if logf != nil {
			logf.Close()
		}
		return errors.New("не удалось запустить движок (нужны права администратора)")
	}
	e.cmd = cmd
	e.dir = dir
	e.log = logf
	e.bin = bin
	return nil
}

// Describe — чем именно запущен движок: путь к бинарю, PID, каталог конфига.
// Нужно, чтобы по журналу отличать «взяли не тот sing-box» от «движок не стартовал».
func (e *Engine) Describe() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd == nil || e.cmd.Process == nil {
		return "не запущен"
	}
	ver := "?"
	if out, err := exec.Command(e.bin, "version").Output(); err == nil {
		if line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); line != "" {
			ver = line
		}
	}
	return fmt.Sprintf("PID %d · %s · конфиг в %s", e.cmd.Process.Pid, ver, e.dir)
}

func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd != nil && e.cmd.Process != nil {
		e.cmd.Process.Kill()
		e.cmd.Wait()
	}
	if e.dir != "" {
		os.RemoveAll(e.dir)
	}
	if e.log != nil {
		e.log.Close()
		e.log = nil
	}
	e.cmd = nil
	e.dir = ""
}
