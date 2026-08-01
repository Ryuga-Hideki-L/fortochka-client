//go:build !windows && !linux

package main

func listRunningApps() []RunningApp { return nil }
