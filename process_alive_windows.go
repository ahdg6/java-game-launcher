//go:build windows

package main

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

var (
	openProcessProc        = kernel32DLL.NewProc("OpenProcess")
	getExitCodeProcessProc = kernel32DLL.NewProc("GetExitCodeProcess")
	closeHandleProc        = kernel32DLL.NewProc("CloseHandle")
)

func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, _, callErr := openProcessProc.Call(processQueryLimitedInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		// Access denied means the process exists but is protected/elevated. Be
		// conservative and keep the recovery marker instead of restoring mods
		// underneath a possibly running game.
		return errors.Is(callErr, syscall.ERROR_ACCESS_DENIED)
	}
	defer closeHandleProc.Call(handle)
	var exitCode uint32
	result, _, _ := getExitCodeProcessProc.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	return result != 0 && exitCode == stillActive
}
