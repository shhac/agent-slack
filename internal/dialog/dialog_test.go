package dialog

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/ncruces/zenity"
)

func TestPromptSecretReturnsValue(t *testing.T) {
	restore := entry
	t.Cleanup(func() { entry = restore })
	entry = func(string, ...zenity.Option) (string, error) { return "s3cret", nil }

	v, err := PromptSecret(context.Background(), "Add token", "Paste xoxc", "")
	if err != nil {
		t.Fatal(err)
	}
	if v != "s3cret" {
		t.Errorf("value = %q, want s3cret", v)
	}
}

func TestPromptSecretWrapsError(t *testing.T) {
	restore := entry
	t.Cleanup(func() { entry = restore })
	entry = func(string, ...zenity.Option) (string, error) { return "", stderrors.New("cancelled") }

	_, err := PromptSecret(context.Background(), "t", "l", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "prompt for secret") || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("err = %q, want it to wrap the backend error", err)
	}
}
