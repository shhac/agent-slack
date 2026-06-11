package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"errors"
	"net/url"
	"regexp"
)

var xoxdValueRe = regexp.MustCompile(`xoxd-[A-Za-z0-9%/+_=.\-]+`)

// decryptChromiumCookie decrypts a Chromium "v10"/"v11" cookie value (already
// stripped of the 3-byte version prefix) using the macOS/Linux scheme:
// PBKDF2-HMAC-SHA1(password, "saltysalt", iterations, 16 bytes) as an
// AES-128-CBC key with a 16-space IV. It returns the recovered xoxd- token,
// URL-decoded.
func decryptChromiumCookie(data []byte, password string, iterations int) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty cookie data")
	}
	if iterations < 1 {
		return "", errors.New("iterations must be >= 1")
	}
	if len(data)%aes.BlockSize != 0 {
		return "", errors.New("cookie data is not a multiple of the AES block size")
	}

	key := pbkdf2SHA1([]byte(password), []byte("saltysalt"), iterations, 16)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := bytes16Spaces()
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return "", err
	}

	return xoxdFromPlain(plain)
}

// xoxdFromPlain finds the xoxd-* token in decrypted cookie bytes, URL-decoded.
func xoxdFromPlain(plain []byte) (string, error) {
	match := xoxdValueRe.Find(plain)
	if match == nil {
		return "", errors.New("no xoxd-* token in decrypted cookie")
	}
	token := string(match)
	if decoded, derr := url.PathUnescape(token); derr == nil {
		return decoded, nil
	}
	return token, nil
}

func bytes16Spaces() []byte {
	iv := make([]byte, 16)
	for i := range iv {
		iv[i] = ' '
	}
	return iv
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid padded data")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("invalid PKCS#7 padding bytes")
		}
	}
	return data[:len(data)-pad], nil
}

// pbkdf2SHA1 is a minimal PBKDF2-HMAC-SHA1 implementation, avoiding a dependency
// on golang.org/x/crypto for this single use.
func pbkdf2SHA1(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	out := make([]byte, 0, numBlocks*hashLen)
	buf := make([]byte, 4)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf)
		u := prf.Sum(nil)

		t := make([]byte, len(u))
		copy(t, u)
		for n := 1; n < iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for i := range t {
				t[i] ^= u[i]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
