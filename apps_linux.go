//go:build linux

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Списки программ на Linux. Раньше здесь была заглушка, возвращавшая nil:
// пользователь Linux не мог выбрать программу из списка и должен был угадать
// имя процесса руками — а угадать его нельзя, потому что sing-box сверяет
// process_name с базовым именем ФАЙЛА из /proc/<pid>/exe, а не с названием в меню
// (google-chrome-stable в меню — процесс chrome, steam — процесс steamwebhelper).
//
// Поэтому источников два и они дополняют друг друга:
//   - .desktop-файлы — понятные названия того, что установлено;
//   - /proc — реальные имена процессов, ровно те, что увидит движок.

var desktopDirs = []string{
	"/usr/share/applications",
	"/usr/local/share/applications",
	"/var/lib/flatpak/exports/share/applications",
	"/var/lib/snapd/desktop/applications",
}

func listInstalledApps() []InstalledApp {
	seen := map[string]bool{}
	var out []InstalledApp

	dirs := append([]string{}, desktopDirs...)
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".local/share/applications"),
			filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		)
	}
	for _, dir := range dirs {
		collectDesktop(dir, seen, &out)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > 500 {
		out = out[:500]
	}
	return out
}

func collectDesktop(dir string, seen map[string]bool, out *[]InstalledApp) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
			continue
		}
		name, exec, noDisplay := parseDesktop(filepath.Join(dir, e.Name()))
		if noDisplay || exec == "" || isStubExec(exec) {
			continue
		}
		if name == "" {
			name = exec
		}
		addApp(seen, out, name, exec)
	}
}

// parseDesktop берёт из .desktop только то, что нужно: видимое имя и бинарь.
// Полноценный парсер ini здесь избыточен — читаем первую секцию [Desktop Entry].
func parseDesktop(path string) (name, exec string, noDisplay bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()

	inEntry := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			if name == "" {
				name = strings.TrimSpace(val)
			}
		case "Exec":
			if exec == "" {
				exec = execBinary(val)
			}
		case "NoDisplay", "Hidden":
			noDisplay = noDisplay || strings.EqualFold(strings.TrimSpace(val), "true")
		case "Type":
			if !strings.EqualFold(strings.TrimSpace(val), "Application") {
				noDisplay = true
			}
		}
	}
	return name, exec, noDisplay
}

// execBinary достаёт имя бинаря из строки Exec: там бывают переменные (%U, %F),
// обёртки (env VAR=1 /usr/bin/foo) и запуск через flatpak.
func execBinary(exec string) string {
	fields := strings.Fields(strings.TrimSpace(exec))
	for len(fields) > 0 {
		f := strings.Trim(fields[0], `"'`)
		base := filepath.Base(f)
		switch {
		case f == "":
			fields = fields[1:]
		case base == "env" || strings.Contains(f, "="):
			// обёртка вида `env LANG=C /usr/bin/foo` — интересен следующий токен
			fields = fields[1:]
		case base == "flatpak" || base == "snap":
			// `flatpak run org.telegram.desktop` — имя процесса отсюда не вывести
			// достоверно, но идентификатор приложения полезнее пустоты
			if len(fields) > 1 {
				id := fields[len(fields)-1]
				if i := strings.LastIndex(id, "."); i >= 0 && i+1 < len(id) {
					return id[i+1:]
				}
			}
			return ""
		case strings.HasPrefix(base, "%"):
			fields = fields[1:]
		default:
			return base
		}
	}
	return ""
}

// isStubExec отсеивает записи-заглушки. Живой пример: snap кладёт
// chromium_daemon.desktop с Name=Chromium Web Browser и Exec=/usr/bin/false —
// в списке это выглядело как вторая копия браузера с именем процесса «false».
func isStubExec(exec string) bool {
	switch filepath.Base(exec) {
	case "false", "true", "sh", "bash", "dash", "gio", "xdg-open":
		return true
	}
	return false
}

func addApp(seen map[string]bool, out *[]InstalledApp, name, exe string) {
	exe = strings.TrimSpace(filepath.Base(exe))
	if exe == "" || seen[exe] {
		return
	}
	seen[exe] = true
	*out = append(*out, InstalledApp{Name: name, Exe: exe})
}
