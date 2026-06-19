package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// bodyEncoder turns string fields into a request body. Two implementations:
// form-urlencoded (the default) and multipart (some internal Slack methods
// silently ignore urlencoded params).
type bodyEncoder func(fields map[string]string) (body []byte, contentType string, err error)

// encodeParam stringifies one API param: nil drops, objects and slices
// JSON-encode, everything else stringifies (matching the TS client).
func encodeParam(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

func encodeForm(fields map[string]string) ([]byte, string, error) {
	values := url.Values{}
	for k, v := range fields {
		values.Set(k, v)
	}
	return []byte(values.Encode()), "application/x-www-form-urlencoded", nil
}

func encodeMultipart(fields map[string]string) ([]byte, string, error) {
	var buf strings.Builder
	w := multipart.NewWriter(&buf)
	// Sorted for deterministic bodies (map iteration order is random).
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if err := w.WriteField(k, fields[k]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return []byte(buf.String()), w.FormDataContentType(), nil
}

// filePart is one binary upload added to a multipart body alongside the string
// fields (e.g. the image for emoji.add).
type filePart struct {
	field       string // form field name (e.g. "image")
	filename    string
	contentType string
	data        []byte
}

// encodeMultipartFile returns a bodyEncoder that writes the string fields plus
// one binary file part. bytes.Buffer (not strings.Builder) keeps the body
// binary-safe.
func encodeMultipartFile(file filePart) bodyEncoder {
	return func(fields map[string]string) ([]byte, string, error) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, k := range slices.Sorted(maps.Keys(fields)) {
			if err := w.WriteField(k, fields[k]); err != nil {
				return nil, "", err
			}
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.field, file.filename))
		h.Set("Content-Type", file.contentType)
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(file.data); err != nil {
			return nil, "", err
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), w.FormDataContentType(), nil
	}
}
