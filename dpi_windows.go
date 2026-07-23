//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow — не показывать чёрное консольное окно движка.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

const inetSettings = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// setSysProxy ставит системный SOCKS-прокси на byedpi (весь трафик пойдёт через десинк).
func setSysProxy(addr string) error {
	if err := exec.Command("reg", "add", inetSettings, "/v", "ProxyServer", "/t", "REG_SZ", "/d", "socks="+addr, "/f").Run(); err != nil {
		return err
	}
	if err := exec.Command("reg", "add", inetSettings, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f").Run(); err != nil {
		return err
	}
	refreshProxy()
	return nil
}

// clearSysProxy снимает системный прокси.
func clearSysProxy() {
	exec.Command("reg", "add", inetSettings, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	refreshProxy()
}

// killStuckProcs — добить зависшие вспомогательные процессы по имени.
func killStuckProcs() {
	for _, n := range []string{"sing-box.exe", "winws.exe", "byedpi.exe"} {
		cmd := exec.Command("taskkill", "/F", "/IM", n)
		hideWindow(cmd)
		cmd.Run()
	}
}

// refreshProxy — уведомить систему/браузеры о смене настроек прокси.
func refreshProxy() {
	proc := syscall.NewLazyDLL("wininet.dll").NewProc("InternetSetOptionW")
	proc.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	proc.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}
