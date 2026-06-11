package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"
)

// parseLocalConfig decodes a raw Slack `localConfig_v2`/`v3` value as stored in
// the Chromium LevelDB. The value may carry a leading Chromium type byte and be
// encoded as UTF-8 or UTF-16LE; we strip the marker, pick the encoding by NUL
// density, and recover the JSON object (falling back to the outermost {...}
// slice when there is leading/trailing noise).
func parseLocalConfig(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("localConfig is empty")
	}

	data := raw
	if b := raw[0]; b == 0x00 || b == 0x01 || b == 0x02 {
		data = raw[1:]
	}

	nulCount := bytes.Count(data, []byte{0})
	encodings := []string{"utf8", "utf16le"}
	if nulCount > len(data)/4 {
		encodings = []string{"utf16le", "utf8"}
	}

	lastErr := errors.New("localConfig not parseable")
	for _, enc := range encodings {
		text := decodeText(data, enc)
		if m, err := decodeConfigObject(text); err == nil {
			return m, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}

func decodeText(data []byte, enc string) string {
	if enc == "utf16le" {
		if len(data)%2 != 0 {
			data = data[:len(data)-1]
		}
		u16 := make([]uint16, len(data)/2)
		for i := range u16 {
			u16[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
		}
		return string(utf16.Decode(u16))
	}
	return string(data)
}

func decodeConfigObject(text string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &m); err == nil {
		return m, nil
	}
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start == -1 || end == -1 || end <= start {
		return nil, errors.New("no JSON object in localConfig")
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// teamsFromLocalConfig extracts Slack workspace tokens from a decoded
// localConfig object.
func teamsFromLocalConfig(cfg map[string]json.RawMessage) []Team {
	teamsRaw, ok := cfg["teams"]
	if !ok {
		return nil
	}
	var teamsObj map[string]json.RawMessage
	if err := json.Unmarshal(teamsRaw, &teamsObj); err != nil {
		return nil
	}
	return teamsFromMap(teamsObj)
}
