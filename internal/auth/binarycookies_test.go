package auth

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildBinaryCookies constructs a minimal valid single-page, single-cookie
// Cookies.binarycookies blob from the documented layout, so the parser is
// tested against an independently-built fixture rather than a captured file.
func buildBinaryCookies(domain, name, path, value string) []byte {
	const headerLen = 56 // size+reserved+flags+reserved + 4 offsets + reserved + expiry + creation
	domainOff := headerLen
	nameOff := domainOff + len(domain) + 1
	pathOff := nameOff + len(name) + 1
	valueOff := pathOff + len(path) + 1
	size := valueOff + len(value) + 1

	le := binary.LittleEndian
	rec := &bytes.Buffer{}
	putLE := func(b *bytes.Buffer, v uint32) {
		tmp := make([]byte, 4)
		le.PutUint32(tmp, v)
		b.Write(tmp)
	}
	putLE(rec, uint32(size))
	putLE(rec, 0)             // reserved
	putLE(rec, 5)             // flags (secure|httpOnly)
	putLE(rec, 0)             // reserved
	putLE(rec, uint32(domainOff))
	putLE(rec, uint32(nameOff))
	putLE(rec, uint32(pathOff))
	putLE(rec, uint32(valueOff))
	rec.Write(make([]byte, 8)) // reserved
	rec.Write(make([]byte, 8)) // expiry (double)
	rec.Write(make([]byte, 8)) // creation (double)
	for _, s := range []string{domain, name, path, value} {
		rec.WriteString(s)
		rec.WriteByte(0)
	}

	page := &bytes.Buffer{}
	page.Write([]byte{0x00, 0x00, 0x01, 0x00}) // page tag
	putLE(page, 1)                             // cookie count
	putLE(page, 16)                            // offset of the lone record (tag+count+offset+footer)
	putLE(page, 0)                             // footer
	page.Write(rec.Bytes())

	file := &bytes.Buffer{}
	file.WriteString("cook")
	putBE := func(v uint32) {
		tmp := make([]byte, 4)
		binary.BigEndian.PutUint32(tmp, v)
		file.Write(tmp)
	}
	putBE(1)                       // page count
	putBE(uint32(page.Len()))      // page size
	file.Write(page.Bytes())
	file.Write(make([]byte, 8))    // trailing checksum/footer
	return file.Bytes()
}

func TestParseBinaryCookies(t *testing.T) {
	data := buildBinaryCookies(".slack.com", "d", "/", "xoxd-safaritest")
	cookies, err := parseBinaryCookies(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Domain != ".slack.com" || c.Name != "d" || c.Value != "xoxd-safaritest" {
		t.Errorf("cookie = %+v", c)
	}
}

func TestParseBinaryCookiesRejectsGarbage(t *testing.T) {
	if _, err := parseBinaryCookies([]byte("nope")); err == nil {
		t.Error("expected error for non-binarycookies data")
	}
	// "cook" magic + a page count of 1 but no page-size table.
	if _, err := parseBinaryCookies([]byte("cook\x00\x00\x00\x01")); err == nil {
		t.Error("expected error for truncated page-size table")
	}
}
