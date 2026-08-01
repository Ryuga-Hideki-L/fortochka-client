//go:build linux

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Проверяем не «список непустой» (в контейнере сборки он законно пуст), а инвариант,
// на котором держится split: имя процесса должно быть базовым именем файла — sing-box
// сверяет process_name именно с ним, и путь или обрезанное имя не совпадут никогда.
func TestRunningAppsAreBareNames(t *testing.T) {
	apps := listRunningApps()
	if len(apps) == 0 {
		t.Skip("процессы не видны (нет прав или окружение без /proc)")
	}
	for _, a := range apps {
		if a.Exe == "" {
			t.Errorf("пустое имя процесса: %+v", a)
		}
		if strings.ContainsRune(a.Exe, filepath.Separator) {
			t.Errorf("в имени процесса путь: %q", a.Exe)
		}
		if a.Count < 1 {
			t.Errorf("%s: некорректный счётчик %d", a.Exe, a.Count)
		}
		if a.Path != "" && filepath.Base(a.Path) != a.Exe {
			t.Errorf("%s: путь %q не соответствует имени", a.Exe, a.Path)
		}
	}
}

func TestExecBinary(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/firefox %u":                  "firefox",
		"env LANG=C /opt/google/chrome/chrome": "chrome",
		`"/usr/bin/telegram-desktop" -- %u`:    "telegram-desktop",
		"flatpak run org.telegram.desktop":     "desktop",
		"/usr/bin/steam %U":                    "steam",
		"":                                     "",
		"%U":                                   "",
	}
	for in, want := range cases {
		if got := execBinary(in); got != want {
			t.Errorf("execBinary(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestInstalledAppsHaveNames(t *testing.T) {
	apps := listInstalledApps()
	if len(apps) == 0 {
		t.Skip("нет .desktop-файлов в этом окружении")
	}
	seen := map[string]bool{}
	for _, a := range apps {
		if a.Exe == "" || a.Name == "" {
			t.Errorf("неполная запись: %+v", a)
		}
		if seen[a.Exe] {
			t.Errorf("дубль по имени процесса: %s", a.Exe)
		}
		seen[a.Exe] = true
	}
}
