//go:build windows

package main

import (
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Запущенные процессы на Windows. Смысл тот же, что и на Linux: sing-box сверяет
// process_name с именем исполняемого файла, а название программы в меню «Пуск»
// с ним совпадает не всегда (Discord запускается как Update.exe, лаунчеры игр
// порождают процесс с другим именем). Список установленного из реестра остаётся,
// но выбирать из реально работающего — надёжнее.
func listRunningApps() []RunningApp {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil
	}

	netPIDs := netConnectionPIDs()
	agg := map[string]*RunningApp{}
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		key := strings.ToLower(name)
		if key != "" && !isSystemProc(key) {
			online := netPIDs[entry.ProcessID]
			if a, ok := agg[key]; ok {
				a.Count++
				a.Net = a.Net || online
			} else {
				agg[key] = &RunningApp{
					Name:  strings.TrimSuffix(name, filepath.Ext(name)),
					Exe:   key,
					Path:  processPath(entry.ProcessID),
					Count: 1,
					Net:   online,
				}
			}
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	out := make([]RunningApp, 0, len(agg))
	for _, a := range agg {
		out = append(out, *a)
	}
	// Наверх — те, кто прямо сейчас в сети: их и заворачивают в туннель.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Net != out[j].Net {
			return out[i].Net
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Exe < out[j].Exe
	})
	return out
}

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	udpTableOwnerPID    = 1 // UDP_TABLE_OWNER_PID
	afInet              = 2 // AF_INET
	afInet6             = 23
)

// netConnectionPIDs — PID процессов, у которых есть открытые TCP/UDP-соединения.
// Windows не даёт этого через снимок процессов, зато отдаёт таблицы соединений
// с владельцем; берём и IPv4, и IPv6, TCP и UDP.
func netConnectionPIDs() map[uint32]bool {
	out := map[uint32]bool{}
	for _, af := range []uint32{afInet, afInet6} {
		collectTablePIDs(procGetExtendedTcpTable, af, tcpTableOwnerPIDAll, 24, out)
		collectTablePIDs(procGetExtendedUdpTable, af, udpTableOwnerPID, 12, out)
	}
	return out
}

// collectTablePIDs разбирает таблицу вручную: структуры MIB_*_TABLE_OWNER_PID —
// это dwNumEntries, а следом массив записей фиксированного размера, где PID лежит
// последним полем. rowSize задаётся вызывающим, потому что у TCP и UDP он разный
// (у TCP есть состояние и адрес назначения, у UDP их нет).
func collectTablePIDs(proc *windows.LazyProc, af uint32, class uintptr, rowSize int, out map[uint32]bool) {
	var size uint32
	// первый вызов — узнать нужный размер буфера
	proc.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(af), class, 0)
	if size == 0 {
		return
	}
	buf := make([]byte, size)
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
		0, uintptr(af), class, 0)
	if r != 0 || size < 4 {
		return
	}
	n := int(*(*uint32)(unsafe.Pointer(&buf[0])))
	for i := 0; i < n; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		pid := *(*uint32)(unsafe.Pointer(&buf[off+rowSize-4]))
		if pid != 0 {
			out[pid] = true
		}
	}
}

// processPath — полный путь процесса. Может не получиться (чужой сеанс, защищённый
// процесс) — тогда пусто, и в интерфейсе просто не будет подсказки о пути.
func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// isSystemProc прячет служебное: системные процессы Windows и наши собственные.
func isSystemProc(exeLower string) bool {
	switch exeLower {
	case "system", "system idle process", "registry", "memory compression",
		"smss.exe", "csrss.exe", "wininit.exe", "services.exe", "lsass.exe",
		"winlogon.exe", "fontdrvhost.exe", "dwm.exe", "svchost.exe", "spoolsv.exe",
		"taskhostw.exe", "ctfmon.exe", "conhost.exe", "dllhost.exe", "runtimebroker.exe",
		"searchindexer.exe", "wudfhost.exe", "audiodg.exe", "sihost.exe",
		"sing-box.exe", "xray.exe", "winws.exe", "fortochka.exe":
		return true
	}
	return false
}
