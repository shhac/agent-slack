//go:build !windows

package auth

import "errors"

func dpapiUnprotect([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI decryption is only available on Windows")
}
