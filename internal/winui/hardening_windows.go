// Package winui: process-level hardening for the Windows host.
package winui

import (
	"syscall"
	"unsafe"
)

const (
	loadLibrarySearchSystem32 = 0x00000800

	swRestore = 9

	errorAlreadyExists syscall.Errno = 183

	instanceMutexName = `Local\GoFastRDP.Instance`
)

var (
	procSetDefaultDllDirectories = kernel32.NewProc("SetDefaultDllDirectories")
	procSetDllDirectoryW         = kernel32.NewProc("SetDllDirectoryW")
	procCreateMutexW             = kernel32.NewProc("CreateMutexW")
)

// SecureDLLSearch restricts LoadLibrary lookups to System32. Without it a
// planted WebView2Loader.dll next to the executable would be loaded by the
// WebView engine, which turns a portable app in a writable folder into a
// code-execution vector.
func SecureDLLSearch() {
	if procSetDefaultDllDirectories.Find() == nil {
		if ok, _, _ := procSetDefaultDllDirectories.Call(loadLibrarySearchSystem32); ok != 0 {
			return
		}
	}
	// Older systems: at least drop the current directory from the search path.
	if empty, err := syscall.UTF16PtrFromString(""); err == nil {
		procSetDllDirectoryW.Call(uintptr(unsafe.Pointer(empty)))
	}
}

// SingleInstance makes sure only one application instance runs per session.
// When another instance already owns the mutex, its window is brought to the
// foreground and acquired is false; release must be called on exit.
func SingleInstance() (release func(), acquired bool) {
	name, err := syscall.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return func() {}, true
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return func() {}, true
	}
	if callErr == errorAlreadyExists {
		syscall.CloseHandle(syscall.Handle(handle))
		focusExistingWindow()
		return func() {}, false
	}
	return func() { syscall.CloseHandle(syscall.Handle(handle)) }, true
}

// focusExistingWindow restores the window of the instance that is already
// running, so a second launch feels like activating the existing app.
func focusExistingWindow() {
	hwnd, _, _ := procFindWindowEx.Call(0, 0, uintptr(unsafe.Pointer(classNamePtr)), 0)
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(hwnd, swRestore)
	procSetForeground.Call(hwnd)
}
