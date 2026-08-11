//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var (
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")
	moveFileExW = kernel32DLL.NewProc("MoveFileExW")
)

// replaceFileAtomic uses the Windows replace-existing primitive. Unlike
// os.Rename on Windows, this atomically replaces an existing destination; the
// write-through flag asks the filesystem to flush before reporting success.
func replaceFileAtomic(oldPath, newPath string) error {
	oldPointer, err := syscall.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newPointer, err := syscall.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExW.Call(
		uintptr(unsafe.Pointer(oldPointer)),
		uintptr(unsafe.Pointer(newPointer)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if result == 0 {
		return fmt.Errorf("MoveFileExW: %w", callErr)
	}
	return nil
}
