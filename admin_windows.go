//go:build windows

package main

import "os"

// isAdmin — запущены ли мы с правами администратора (нужны для TUN).
// Открытие \\.\PHYSICALDRIVE0 удаётся только под админом.
func isAdmin() bool {
	f, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	if err == nil {
		f.Close()
		return true
	}
	return false
}
