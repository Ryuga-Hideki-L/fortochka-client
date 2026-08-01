//go:build !windows && !linux

package main

func listInstalledApps() []InstalledApp {
	return nil
}
