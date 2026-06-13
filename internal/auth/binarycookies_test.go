package auth

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func putLE32(b *bytes.Buffer, v uint32) {
	tmp := make([]byte, 4)
	binary.LittleEndian.PutUint32(tmp, v)
	b.Write(tmp)
}

func putBE32(b *bytes.Buffer, v uint32) {
	tmp := make([]byte, 4)
	binary.BigEndian.PutUint32(tmp, v)
	b.Write(tmp)
}

// buildCookieRecord encodes one cookie record per the documented layout:
// size + reserved + flags + reserved, four offsets (domain/name/path/value,
// relative to the record start), 8 reserved + expiry + creation doubles, then
// the NUL-terminated strings.
func buildCookieRecord(domain, name, path, value string) []byte {
	const headerLen = 56
	domainOff := headerLen
	nameOff := domainOff + len(domain) + 1
	pathOff := nameOff + len(name) + 1
	valueOff := pathOff + len(path) + 1
	size := valueOff + len(value) + 1

	rec := &bytes.Buffer{}
	putLE32(rec, uint32(size))
	putLE32(rec, 0) // reserved
	putLE32(rec, 5) // flags (secure|httpOnly)
	putLE32(rec, 0) // reserved
	putLE32(rec, uint32(domainOff))
	putLE32(rec, uint32(nameOff))
	putLE32(rec, uint32(pathOff))
	putLE32(rec, uint32(valueOff))
	rec.Write(make([]byte, 24)) // reserved(8) + expiry(8) + creation(8)
	for _, s := range []string{domain, name, path, value} {
		rec.WriteString(s)
		rec.WriteByte(0)
	}
	return rec.Bytes()
}

// buildCookiePage wraps records in a page: tag, little-endian cookie count, a
// count-length table of record offsets (from the page start), a footer, then
// the records.
func buildCookiePage(records ...[]byte) []byte {
	headerLen := 4 + 4 + 4*len(records) + 4
	page := &bytes.Buffer{}
	page.Write([]byte{0x00, 0x00, 0x01, 0x00}) // tag
	putLE32(page, uint32(len(records)))
	off := headerLen
	for _, r := range records {
		putLE32(page, uint32(off))
		off += len(r)
	}
	putLE32(page, 0) // footer
	for _, r := range records {
		page.Write(r)
	}
	return page.Bytes()
}

// buildBinaryCookiesFile assembles pages into a full file: "cook" magic,
// big-endian page count and per-page sizes, then the pages and a trailing
// footer.
func buildBinaryCookiesFile(pages ...[]byte) []byte {
	file := &bytes.Buffer{}
	file.WriteString("cook")
	putBE32(file, uint32(len(pages)))
	for _, p := range pages {
		putBE32(file, uint32(len(p)))
	}
	for _, p := range pages {
		file.Write(p)
	}
	file.Write(make([]byte, 8)) // trailing checksum/footer
	return file.Bytes()
}

// buildBinaryCookies is the single-page, single-cookie convenience used by the
// happy-path test. Its record sits at file offset 28 (12-byte file header +
// 16-byte page header), which the bounds tests rely on.
func buildBinaryCookies(domain, name, path, value string) []byte {
	return buildBinaryCookiesFile(buildCookiePage(buildCookieRecord(domain, name, path, value)))
}

const singleCookieRecordStart = 28 // file header (12) + page header (16)

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

// TestParseBinaryCookiesMultiPageMultiCookie exercises the page loop, the
// multi-entry offset table, and cross-page accumulation — none of which the
// single-cookie fixture reaches.
func TestParseBinaryCookiesMultiPageMultiCookie(t *testing.T) {
	page1 := buildCookiePage(
		buildCookieRecord(".slack.com", "d", "/", "xoxd-one"),
		buildCookieRecord(".example.com", "sid", "/", "abc123"),
	)
	page2 := buildCookiePage(
		buildCookieRecord(".slack.com", "x", "/", "yyy"),
	)
	cookies, err := parseBinaryCookies(buildBinaryCookiesFile(page1, page2))
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 3 {
		t.Fatalf("got %d cookies, want 3: %+v", len(cookies), cookies)
	}
	want := map[string]string{"d": "xoxd-one", "sid": "abc123", "x": "yyy"}
	for _, c := range cookies {
		if want[c.Name] != c.Value {
			t.Errorf("cookie %q = %q, want %q", c.Name, c.Value, want[c.Name])
		}
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
	// A page declared larger than the bytes that follow it.
	oversized := &bytes.Buffer{}
	oversized.WriteString("cook")
	putBE32(oversized, 1)    // one page
	putBE32(oversized, 9999) // claims 9999 bytes
	oversized.Write([]byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00})
	if _, err := parseBinaryCookies(oversized.Bytes()); err == nil {
		t.Error("expected error for a page larger than the remaining data")
	}
}

// TestParseBinaryCookiesRejectsBadRecord drives the record-level bounds checks
// by corrupting fields of an otherwise-valid single-cookie blob.
func TestParseBinaryCookiesRejectsBadRecord(t *testing.T) {
	corrupt := func(at int, v uint32) []byte {
		data := append([]byte(nil), buildBinaryCookies(".slack.com", "d", "/", "xoxd-safaritest")...)
		binary.LittleEndian.PutUint32(data[at:at+4], v)
		return data
	}
	// size field (record+0) set to 0 → "bad cookie record size".
	if _, err := parseBinaryCookies(corrupt(singleCookieRecordStart, 0)); err == nil {
		t.Error("expected error for a zero record size")
	}
	// value offset field (record+28) pointing past the record → out of range.
	if _, err := parseBinaryCookies(corrupt(singleCookieRecordStart+28, 0xFFFF)); err == nil {
		t.Error("expected error for an out-of-range value offset")
	}
}

// TestParseBinaryCookiesTruncationsNeverPanic asserts the bounds checks hold:
// no truncation of a valid file may panic, however the offsets and lengths fall.
func TestParseBinaryCookiesTruncationsNeverPanic(t *testing.T) {
	full := buildBinaryCookies(".slack.com", "d", "/", "xoxd-safaritest")
	for n := 0; n <= len(full); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic parsing the first %d bytes: %v", n, r)
				}
			}()
			_, _ = parseBinaryCookies(full[:n])
		}()
	}
}
