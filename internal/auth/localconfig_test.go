package auth

import (
	"encoding/json"
	"testing"
	"unicode/utf16"
)

const sampleConfig = `{"teams":{"T1":{"url":"https://acme.slack.com/","name":"Acme","token":"xoxc-aaa"},"T2":{"url":"https://globex.slack.com/","name":"Globex","token":"xoxc-bbb"},"T3":{"url":"https://nope.slack.com/","name":"NoToken","token":"xoxb-not-browser"}}}`

func TestParseLocalConfigUTF8WithMarker(t *testing.T) {
	raw := append([]byte{0x01}, []byte(sampleConfig)...)
	cfg, err := parseLocalConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	teams := teamsFromLocalConfig(cfg)
	if len(teams) != 2 {
		t.Fatalf("expected 2 xoxc teams (xoxb filtered out), got %d: %+v", len(teams), teams)
	}
	if teams[0].URL != "https://acme.slack.com/" || teams[0].Token != "xoxc-aaa" {
		t.Errorf("first team wrong: %+v", teams[0])
	}
}

func TestParseLocalConfigUTF16LE(t *testing.T) {
	u16 := utf16.Encode([]rune(sampleConfig))
	raw := make([]byte, 0, len(u16)*2+1)
	raw = append(raw, 0x00) // Chromium UTF-16 marker
	for _, c := range u16 {
		raw = append(raw, byte(c), byte(c>>8))
	}
	cfg, err := parseLocalConfig(raw)
	if err != nil {
		t.Fatalf("utf16 parse failed: %v", err)
	}
	if len(teamsFromLocalConfig(cfg)) != 2 {
		t.Errorf("expected 2 teams from utf16 config")
	}
}

func TestParseLocalConfigWithSurroundingNoise(t *testing.T) {
	raw := []byte("\x00\x00garbage" + sampleConfig + "trailing\x00")
	cfg, err := parseLocalConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(teamsFromLocalConfig(cfg)) != 2 {
		t.Errorf("expected brace-slice recovery to find 2 teams")
	}
}

func TestParseLocalConfigEmpty(t *testing.T) {
	if _, err := parseLocalConfig(nil); err == nil {
		t.Error("expected error on empty input")
	}
}

func TestParseTeamsJSON(t *testing.T) {
	raw := []byte(`{"T1":{"url":"https://a.slack.com/","token":"xoxc-1"},"T2":{"url":"https://b.slack.com/","token":"nope"}}`)
	teams := parseTeamsJSON(raw)
	if len(teams) != 1 || teams[0].Token != "xoxc-1" {
		t.Errorf("parseTeamsJSON = %+v", teams)
	}
}

func TestTeamsFromMapSortedAndFiltered(t *testing.T) {
	m := map[string]json.RawMessage{
		"b": json.RawMessage(`{"url":"https://b.slack.com/","token":"xoxc-b"}`),
		"a": json.RawMessage(`{"url":"https://a.slack.com/","token":"xoxc-a"}`),
		"x": json.RawMessage(`{"url":"","token":"xoxc-x"}`),
	}
	teams := teamsFromMap(m)
	if len(teams) != 2 || teams[0].URL != "https://a.slack.com/" {
		t.Errorf("teamsFromMap = %+v", teams)
	}
}
