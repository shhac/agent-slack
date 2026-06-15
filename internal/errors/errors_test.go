package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"
)

func TestNewAndNewf(t *testing.T) {
	e := New("boom", FixableByAgent)
	if e.Message != "boom" || e.Error() != "boom" || e.FixableBy != FixableByAgent {
		t.Errorf("New = %+v", e)
	}
	f := Newf(FixableByRetry, "got %d of %s", 3, "items")
	if f.Message != "got 3 of items" || f.FixableBy != FixableByRetry {
		t.Errorf("Newf = %+v", f)
	}
}

// Wrap(nil) must return a genuine nil so callers can `return Wrap(maybeErr, …)`
// and have a nil error mean success.
func TestWrapNilIsNil(t *testing.T) {
	if Wrap(nil, FixableByAgent) != nil {
		t.Fatal("Wrap(nil) should be nil")
	}
}

func TestWrapPreservesCause(t *testing.T) {
	cause := stderrors.New("root cause")
	e := Wrap(cause, FixableByRetry)
	if e.Message != "root cause" || e.FixableBy != FixableByRetry {
		t.Errorf("Wrap = %+v", e)
	}
	if e.Unwrap() != cause || !stderrors.Is(e, cause) {
		t.Errorf("Unwrap/Is did not reach the cause")
	}
}

// WithHint sets the hint and returns the same pointer, so it chains onto New/Newf.
func TestWithHintChains(t *testing.T) {
	e := New("x", FixableByAgent)
	if got := e.WithHint("do y"); got != e || e.Hint != "do y" {
		t.Errorf("WithHint = %+v (same ptr: %v)", e, got == e)
	}
}

func TestAsUnwrapsToAPIError(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", New("inner", FixableByHuman))
	var apiErr *APIError
	if !As(wrapped, &apiErr) || apiErr.Message != "inner" || apiErr.FixableBy != FixableByHuman {
		t.Errorf("As did not unwrap to the APIError: %v", apiErr)
	}
}

func TestJSONShape(t *testing.T) {
	withHint := mustUnmarshal(t, New("oops", FixableByAgent).WithHint("try this"))
	if withHint["error"] != "oops" || withHint["fixable_by"] != "agent" || withHint["hint"] != "try this" {
		t.Errorf("json = %v", withHint)
	}
	if _, ok := withHint["Cause"]; ok {
		t.Error("Cause must not serialize")
	}

	noHint := mustUnmarshal(t, New("oops", FixableByAgent))
	if _, ok := noHint["hint"]; ok {
		t.Error("an empty hint must be omitted")
	}
}

func mustUnmarshal(t *testing.T, e *APIError) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
