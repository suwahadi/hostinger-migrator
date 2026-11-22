package main

import "golang.org/x/sys/windows"

func init() {
	windows.SetConsoleOutputCP(65001)
	windows.SetConsoleCP(65001)
}
