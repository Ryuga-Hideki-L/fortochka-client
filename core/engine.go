package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// Engine управляет процессом sing-box.
type Engine struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	dir string
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
	logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)

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
	return nil
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
