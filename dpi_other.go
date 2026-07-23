//go:build !windows

package main

import "os/exec"

// На не-Windows «Запрет» — заглушки (фича заточена под Windows/WinDivert/byedpi).
func hideWindow(cmd *exec.Cmd)      {}
func setSysProxy(addr string) error { return nil }
func clearSysProxy()                {}

func killStuckProcs() {
	for _, n := range []string{"sing-box", "winws", "byedpi"} {
		exec.Command("pkill", "-f", n).Run()
	}
}
