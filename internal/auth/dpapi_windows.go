package auth

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiUnprotect decrypts a DPAPI blob in the current user's context via
// CryptUnprotectData.
func dpapiUnprotect(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("empty DPAPI blob")
	}
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) //nolint:errcheck
	plain := make([]byte, out.Size)
	copy(plain, unsafe.Slice(out.Data, out.Size))
	return plain, nil
}
