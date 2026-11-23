package main

import (
	"golang.org/x/sys/windows"

	"hostinger-migrator/internal/cli"
)

func init() {
	windows.SetConsoleOutputCP(65001)
	windows.SetConsoleCP(65001)
	if !enableVT(windows.STD_OUTPUT_HANDLE) || !enableVT(windows.STD_ERROR_HANDLE) {
		cli.ANSIDisabled = true
	}
}

func enableVT(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil {
		return false
	}
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
