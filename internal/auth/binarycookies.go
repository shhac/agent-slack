package auth

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// binaryCookie is one decoded entry from a Safari Cookies.binarycookies file.
type binaryCookie struct {
	Domain string
	Name   string
	Value  string
}

// parseBinaryCookies decodes Safari's Cookies.binarycookies format (WebKit).
// Layout: magic "cook", big-endian page count and page sizes, then each page
// holds little-endian cookie records with offset-addressed NUL-terminated
// strings. Only the fields we need (domain, name, value) are recovered; bounds
// are checked throughout so a truncated or hostile file yields an error rather
// than a panic.
func parseBinaryCookies(data []byte) ([]binaryCookie, error) {
	if len(data) < 8 || !bytes.HasPrefix(data, []byte("cook")) {
		return nil, errors.New("not a Cookies.binarycookies file")
	}
	numPages := binary.BigEndian.Uint32(data[4:8])
	off := 8
	pageSizes := make([]uint32, 0, numPages)
	for i := uint32(0); i < numPages; i++ {
		if off+4 > len(data) {
			return nil, errors.New("binarycookies: truncated page-size table")
		}
		pageSizes = append(pageSizes, binary.BigEndian.Uint32(data[off:off+4]))
		off += 4
	}

	var cookies []binaryCookie
	for _, size := range pageSizes {
		if off+int(size) > len(data) {
			return nil, errors.New("binarycookies: truncated page")
		}
		page := data[off : off+int(size)]
		off += int(size)
		pc, err := parseBinaryCookiePage(page)
		if err != nil {
			return nil, err
		}
		cookies = append(cookies, pc...)
	}
	return cookies, nil
}

func parseBinaryCookiePage(page []byte) ([]binaryCookie, error) {
	// Page header: 4-byte tag (0x00000100), little-endian cookie count, then a
	// count-length table of little-endian cookie offsets from the page start.
	if len(page) < 8 {
		return nil, errors.New("binarycookies: short page header")
	}
	numCookies := binary.LittleEndian.Uint32(page[4:8])
	offsets := make([]uint32, 0, numCookies)
	pos := 8
	for i := uint32(0); i < numCookies; i++ {
		if pos+4 > len(page) {
			return nil, errors.New("binarycookies: truncated cookie-offset table")
		}
		offsets = append(offsets, binary.LittleEndian.Uint32(page[pos:pos+4]))
		pos += 4
	}

	var out []binaryCookie
	for _, co := range offsets {
		c, err := parseBinaryCookieRecord(page, int(co))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func parseBinaryCookieRecord(page []byte, start int) (binaryCookie, error) {
	// Record: size, 4 reserved, flags, 4 reserved, then domain/name/path/value
	// offsets (each relative to the record start), 8 reserved, 8-byte expiry and
	// creation doubles, then the NUL-terminated strings.
	if start < 0 || start+40 > len(page) {
		return binaryCookie{}, errors.New("binarycookies: cookie record out of range")
	}
	size := int(binary.LittleEndian.Uint32(page[start : start+4]))
	if size < 40 || start+size > len(page) {
		return binaryCookie{}, errors.New("binarycookies: bad cookie record size")
	}
	rec := page[start : start+size]
	domainOff := binary.LittleEndian.Uint32(rec[16:20])
	nameOff := binary.LittleEndian.Uint32(rec[20:24])
	valueOff := binary.LittleEndian.Uint32(rec[28:32])

	domain, err := cstringAt(rec, int(domainOff))
	if err != nil {
		return binaryCookie{}, err
	}
	name, err := cstringAt(rec, int(nameOff))
	if err != nil {
		return binaryCookie{}, err
	}
	value, err := cstringAt(rec, int(valueOff))
	if err != nil {
		return binaryCookie{}, err
	}
	return binaryCookie{Domain: domain, Name: name, Value: value}, nil
}

// cstringAt reads a NUL-terminated string at off within rec.
func cstringAt(rec []byte, off int) (string, error) {
	if off < 0 || off >= len(rec) {
		return "", errors.New("binarycookies: string offset out of range")
	}
	end := bytes.IndexByte(rec[off:], 0)
	if end < 0 {
		return "", errors.New("binarycookies: unterminated string")
	}
	return string(rec[off : off+end]), nil
}
