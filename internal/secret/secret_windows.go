// Package secret wraps Windows DPAPI so credentials are encrypted at rest
// with the current user's account key.
package secret

import (
	"encoding/base64"
	"errors"
	"syscall"
	"unsafe"
)

var (
	crypt32       = syscall.NewLazyDLL("crypt32.dll")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procProtect   = crypt32.NewProc("CryptProtectData")
	procUnprot    = crypt32.NewProc("CryptUnprotectData")
	procLocalFree = kernel32.NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

// Protect encrypts plain with the current user's DPAPI key and returns base64.
func Protect(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	blob, err := protectBytes([]byte(plain))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(blob), nil
}

// Unprotect reverses Protect.
func Unprotect(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	plain, err := unprotectBytes(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ProtectRaw exposes the DPAPI encryption for callers that need the raw
// protected blob (for example the RDP file password field).
func ProtectRaw(b []byte) ([]byte, error) { return protectBytes(b) }

func protectBytes(b []byte) ([]byte, error) {
	in := newBlob(b)
	var out dataBlob
	ret, _, err := procProtect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, errors.New("CryptProtectData: " + err.Error())
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return copyBlob(out), nil
}

func unprotectBytes(b []byte) ([]byte, error) {
	in := newBlob(b)
	var out dataBlob
	ret, _, err := procUnprot.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, errors.New("CryptUnprotectData: " + err.Error())
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return copyBlob(out), nil
}

func copyBlob(b dataBlob) []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}
