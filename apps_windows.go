//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func listInstalledApps() []InstalledApp {
	seen := map[string]bool{}
	var out []InstalledApp

	collectUninstall(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, seen, &out)
	collectUninstall(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, seen, &out)
	collectUninstall(registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, seen, &out)

	for _, dir := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if dir == "" {
			continue
		}
		collectProgramFiles(dir, seen, &out)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > 500 {
		out = out[:500]
	}
	return out
}

func collectUninstall(root registry.Key, path string, seen map[string]bool, out *[]InstalledApp) {
	k, err := registry.OpenKey(root, path, registry.READ|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return
	}
	for _, name := range names {
		sub, err := registry.OpenKey(k, name, registry.READ)
		if err != nil {
			continue
		}
		display, _, _ := sub.GetStringValue("DisplayName")
		icon, _, _ := sub.GetStringValue("DisplayIcon")
		loc, _, _ := sub.GetStringValue("InstallLocation")
		sub.Close()
		display = strings.TrimSpace(display)
		if display == "" {
			continue
		}
		exe := exeFromIcon(icon)
		if exe == "" && loc != "" {
			exe = findExeInDir(loc)
		}
		if exe == "" {
			continue
		}
		addApp(seen, out, display, exe)
	}
}

func collectProgramFiles(root string, seen map[string]bool, out *[]InstalledApp) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		exe := findExeInDir(dir)
		if exe == "" {
			continue
		}
		addApp(seen, out, e.Name(), exe)
	}
}

func findExeInDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".exe") {
			return filepath.Base(e.Name())
		}
	}
	return ""
}

func exeFromIcon(icon string) string {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		return ""
	}
	if i := strings.Index(icon, ","); i >= 0 {
		icon = icon[:i]
	}
	icon = strings.Trim(icon, `"`)
	if !strings.HasSuffix(strings.ToLower(icon), ".exe") {
		return ""
	}
	return filepath.Base(icon)
}

func addApp(seen map[string]bool, out *[]InstalledApp, name, exe string) {
	exe = strings.ToLower(filepath.Base(exe))
	if exe == "" || seen[exe] {
		return
	}
	seen[exe] = true
	*out = append(*out, InstalledApp{Name: name, Exe: exe})
}
