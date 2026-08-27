//go:build windows

package mindustry

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
	processKernel32        = syscall.NewLazyDLL("kernel32.dll")
	openProcessProc        = processKernel32.NewProc("OpenProcess")
	getExitCodeProcessProc = processKernel32.NewProc("GetExitCodeProcess")
	closeHandleProc        = processKernel32.NewProc("CloseHandle")
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
