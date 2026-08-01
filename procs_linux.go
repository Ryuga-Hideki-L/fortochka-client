//go:build linux

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Запущенные процессы — самый честный источник имён для split: sing-box сверяет
// process_name ровно с базовым именем файла из /proc/<pid>/exe. Название в меню
// («Google Chrome») и имя процесса (chrome) совпадают далеко не всегда, поэтому
// пользователь выбирает из того, что действительно работает прямо сейчас.
//
// /proc/<pid>/comm не годится как основной источник: ядро обрезает его до 15
// символов, и, например, telegram-desktop превращается в telegram-deskt —
// такое правило не совпадёт никогда.
func listRunningApps() []RunningApp {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := filepath.Base(os.Args[0])
	netInodes := socketInodes()
	agg := map[string]*RunningApp{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if err != nil {
			// без root чужие процессы не читаются — это не ошибка, просто пропуск
			continue
		}
		// у удалённого бинаря ядро дописывает « (deleted)»
		exe = strings.TrimSuffix(exe, " (deleted)")
		name := filepath.Base(exe)
		if name == "" || name == "." || isSystemProc(name, exe) || name == self {
			continue
		}
		online := hasNetSocket(e.Name(), netInodes)
		if a, ok := agg[name]; ok {
			a.Count++
			a.Net = a.Net || online
			continue
		}
		agg[name] = &RunningApp{Name: name, Exe: name, Path: exe, Count: 1, Net: online}
	}

	out := make([]RunningApp, 0, len(agg))
	for _, a := range agg {
		out = append(out, *a)
	}
	// Наверх — те, кто прямо сейчас в сети: заворачивают в туннель именно их.
	// Дальше по числу процессов, потом по алфавиту.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Net != out[j].Net {
			return out[i].Net
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// socketInodes — inode всех открытых TCP/UDP-сокетов системы. Ядро не даёт прямой
// связи «процесс → соединение», поэтому сопоставление идёт через inode: здесь
// собираем номера из /proc/net/*, ниже сверяем с дескрипторами процессов.
func socketInodes() map[string]bool {
	out := map[string]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6", "/proc/net/udp", "/proc/net/udp6"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if i == 0 {
				continue // заголовок
			}
			fields := strings.Fields(line)
			// inode — десятое поле; строки короче просто пропускаем
			if len(fields) < 10 {
				continue
			}
			if ino := fields[9]; ino != "" && ino != "0" {
				out[ino] = true
			}
		}
	}
	return out
}

// hasNetSocket — есть ли у процесса хоть один сетевой сокет. Дескрипторы чужих
// процессов без root не читаются: тогда просто вернётся false, и программа
// окажется ниже в списке, но из него не исчезнет.
func hasNetSocket(pid string, netInodes map[string]bool) bool {
	if len(netInodes) == 0 {
		return false
	}
	fdDir := filepath.Join("/proc", pid, "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		ino := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
		if netInodes[ino] {
			return true
		}
	}
	return false
}

// isSystemProc отсеивает то, что человеку выбирать бессмысленно: ядровые потоки,
// демоны из /usr/sbin и служебные процессы самого дистрибутива. Список короткий
// намеренно — лучше показать лишнее, чем спрятать нужное.
func isSystemProc(name, path string) bool {
	if strings.HasPrefix(path, "/usr/sbin/") || strings.HasPrefix(path, "/sbin/") {
		return true
	}
	switch name {
	case "systemd", "systemd-journald", "systemd-udevd", "systemd-logind",
		"systemd-resolved", "systemd-timesyncd", "dbus-daemon", "dbus-broker",
		"kthreadd", "kworker", "ksoftirqd", "migration", "rcu_sched",
		"sing-box", "xray", "sudo", "su", "bash", "sh", "zsh", "dash":
		return true
	}
	return false
}
