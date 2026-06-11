package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	root := newRootCmd("1.2.3")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "1.2.3") {
		t.Errorf("version output %q does not contain 1.2.3", buf.String())
	}
}

func TestUsageCommand(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd("dev")
	root.SetOut(&out)
	root.SetArgs([]string{"usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fixable_by") {
		t.Errorf("usage output should document the error contract, got %q", out.String())
	}
}

func TestUnknownCommandErrorContract(t *testing.T) {
	var errBuf bytes.Buffer
	root := newRootCmd("dev")
	root.SetErr(&errBuf)
	root.SetArgs([]string{"definitely-not-a-command"})
	if err := execute(root); err == nil {
		t.Fatal("expected error for unknown command")
	}

	var payload map[string]any
	if err := json.Unmarshal(errBuf.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not valid JSON: %v (got %q)", err, errBuf.String())
	}
	if payload["fixable_by"] == "" {
		t.Error("expected fixable_by in error payload")
	}
}
