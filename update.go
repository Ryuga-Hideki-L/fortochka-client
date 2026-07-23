package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// version инжектится при сборке: -ldflags "-X main.version=1.0.7". По умолчанию "dev".
var version = "dev"

const (
	releasesAPI = "https://api.github.com/repos/Ryuga-Hideki-L/fortochka-client/releases/latest"
	releasesURL = "https://github.com/Ryuga-Hideki-L/fortochka-client/releases/latest"
)

// UpdateInfo отдаётся во фронт для баннера обновления.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	HasUpdate bool   `json:"hasUpdate"`
	URL       string `json:"url"`
}

// Version — текущая версия для UI.
func (a *App) Version() string { return version }

// CheckUpdate спрашивает GitHub про последний релиз и сравнивает с текущей версией.
func (a *App) CheckUpdate() UpdateInfo {
	ui := UpdateInfo{Current: version, Latest: version}
	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", releasesAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		a.setUpd("не удалось проверить обновления (нет связи с GitHub)")
		return ui
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		a.setUpd(fmt.Sprintf("не удалось проверить обновления (GitHub ответил %d)", resp.StatusCode))
		return ui
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.NewDecoder(resp.Body).Decode(&rel) != nil {
		a.setUpd("не удалось проверить обновления (ответ GitHub не разобран)")
		return ui
	}
	ui.Latest = strings.TrimPrefix(rel.TagName, "v")
	suffix := ".zip"
	if runtime.GOOS != "windows" {
		suffix = "-linux.tar.gz"
	}
	for _, as := range rel.Assets {
		if strings.HasSuffix(as.Name, suffix) {
			ui.URL = as.URL
			break
		}
	}
	ui.HasUpdate = ui.URL != "" && semverNewer(ui.Latest, version)
	if ui.HasUpdate {
		a.setUpd(fmt.Sprintf("доступно обновление: %s → %s", version, ui.Latest))
	} else {
		a.setUpd(fmt.Sprintf("версия актуальная (%s), обновлений нет", version))
	}
	return ui
}

// setUpd сохраняет статус обновления и сразу пишет его в журнал.
func (a *App) setUpd(note string) {
	a.mu.Lock()
	a.updNote = note
	a.mu.Unlock()
	a.log("%s", note)
}

// DoUpdate скачивает и ставит новую версию. На Windows — самозамена с перезапуском.
func (a *App) DoUpdate() string {
	ui := a.CheckUpdate()
	if !ui.HasUpdate || ui.URL == "" {
		return "Обновление не найдено"
	}
	if runtime.GOOS != "windows" {
		_ = exec.Command("xdg-open", releasesURL).Start()
		return "Откройте страницу загрузки в браузере"
	}

	a.log("обновление до %s: скачиваю…", ui.Latest)
	a.Disconnect() // освободить sing-box.exe для замены

	tmp, err := os.MkdirTemp("", "fortochka-upd")
	if err != nil {
		return "Не удалось подготовить обновление"
	}
	zipPath := filepath.Join(tmp, "upd.zip")
	if err := downloadFile(ui.URL, zipPath); err != nil {
		return "Не удалось скачать обновление"
	}
	if err := unzipFlat(zipPath, tmp); err != nil {
		return "Не удалось распаковать обновление"
	}
	exe, err := os.Executable()
	if err != nil {
		return "Не удалось найти папку приложения"
	}
	dir := filepath.Dir(exe)
	newExe := filepath.Join(tmp, "Fortochka.exe")
	newSB := filepath.Join(tmp, "sing-box.exe")
	if _, err := os.Stat(newExe); err != nil {
		return "В обновлении нет Fortochka.exe"
	}

	// батник: ждёт выхода приложения, заменяет файлы, перезапускает
	bat := filepath.Join(tmp, "upd.bat")
	script := "@echo off\r\n" +
		"timeout /t 2 /nobreak >nul\r\n" +
		fmt.Sprintf("copy /y \"%s\" \"%s\" >nul\r\n", newExe, filepath.Join(dir, "Fortochka.exe")) +
		fmt.Sprintf("if exist \"%s\" copy /y \"%s\" \"%s\" >nul\r\n", newSB, newSB, filepath.Join(dir, "sing-box.exe")) +
		fmt.Sprintf("start \"\" \"%s\"\r\n", filepath.Join(dir, "Fortochka.exe"))
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return "Не удалось записать апдейтер"
	}
	cmd := exec.Command("cmd", "/c", "start", "", "/min", bat)
	hideWindow(cmd) // не показывать чёрную консоль апдейтера
	if err := cmd.Start(); err != nil {
		return "Не удалось запустить апдейтер"
	}
	a.log("обновление скачано, перезапуск…")
	go func() { time.Sleep(400 * time.Millisecond); os.Exit(0) }()
	return ""
}

func semverNewer(a, b string) bool {
	pa, pb := semverParts(a), semverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func semverParts(s string) [3]int {
	var p [3]int
	segs := strings.SplitN(strings.TrimPrefix(s, "v"), ".", 3)
	for i := 0; i < len(segs) && i < 3; i++ {
		digits := strings.TrimFunc(segs[i], func(r rune) bool { return r < '0' || r > '9' })
		n, _ := strconv.Atoi(digits)
		p[i] = n
	}
	return p
}

func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// unzipFlat распаковывает файлы архива в dst без вложенных путей.
func unzipFlat(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(dst, filepath.Base(f.Name)))
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			return cerr
		}
	}
	return nil
}
