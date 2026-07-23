//go:build !windows

package main

import "os"

// isAdmin — на Linux/macOS root нужен для TUN.
func isAdmin() bool { return os.Geteuid() == 0 }
