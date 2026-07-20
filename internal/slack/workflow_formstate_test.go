package slack

import (
	"strings"
	"testing"
)

func TestValidateWorkflowFields(t *testing.T) {
	schema := testSchema()
	if errs := ValidateWorkflowFields(map[string]string{"summary": "x"}, schema); len(errs) != 0 {
		t.Errorf("case-insensitive title should validate: %v", errs)
	}
	errs := ValidateWorkflowFields(map[string]string{"Nope": "x"}, schema)
	if len(errs) != 2 { // unknown field + missing required Summary
		t.Errorf("errs = %v", errs)
	}
	if !strings.Contains(errs[0], "Available: Summary, Priority") {
		t.Errorf("unknown-field error should enumerate titles: %v", errs[0])
	}
}

func TestBuildFormState(t *testing.T) {
	view := map[string]any{
		"blocks": []any{
			map[string]any{"block_id": "blk1", "element": map[string]any{"action_id": "field-uuid-1"}},
			map[string]any{"block_id": "blk2", "element": map[string]any{"action_id": "field-uuid-2"}},
			map[string]any{"block_id": "blk3", "element": map[string]any{"action_id": "unknown-uuid"}},
			map[string]any{"element": map[string]any{"action_id": "field-uuid-1"}}, // no block_id
		},
	}
	state, titles, err := buildFormState(view, testSchema(), map[string]string{"summary": "deploy failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 1 { // only blk1: blk2 has no user value, blk3 unknown field
		t.Fatalf("state = %v", state)
	}
	entry := state["blk1"].(map[string]any)["field-uuid-1"].(map[string]any)
	if entry["value"] != "deploy failed" || entry["type"] != "plain_text_input" {
		t.Errorf("entry = %v", entry)
	}
	// The title mapping covers every schema-resolved block, filled or not.
	if titles["blk1"] != "Summary" || titles["blk2"] != "Priority" {
		t.Errorf("titles = %v", titles)
	}
}

func TestBuildFormStateUnmatchedField(t *testing.T) {
	view := map[string]any{"blocks": []any{}} // stub view: no blocks at all
	_, _, err := buildFormState(view, testSchema(), map[string]string{"summary": "x"})
	if err == nil || !strings.Contains(err.Error(), "not present in the opened form") {
		t.Fatalf("a supplied field with no matching block must error, got %v", err)
	}
}

func selectElement(elemType string) map[string]any {
	return map[string]any{
		"type": elemType,
		"options": []any{
			map[string]any{
				"text":  map[string]any{"type": "plain_text", "text": "Low"},
				"value": "opt-low",
			},
			map[string]any{
				"text":  map[string]any{"type": "plain_text", "text": "High"},
				"value": "opt-high",
			},
		},
	}
}

func TestFormStateEntryTypes(t *testing.T) {
	t.Run("rich text wraps the value in a rich_text document", func(t *testing.T) {
		entry, err := formStateEntry(map[string]any{"type": "rich_text_input"}, "Notes", "hello")
		if err != nil {
			t.Fatal(err)
		}
		doc := entry["rich_text_value"].(map[string]any)
		section := doc["elements"].([]any)[0].(map[string]any)
		text := section["elements"].([]any)[0].(map[string]any)
		if doc["type"] != "rich_text" || section["type"] != "rich_text_section" || text["text"] != "hello" {
			t.Errorf("entry = %v", entry)
		}
	})

	t.Run("select matches by label case-insensitively and copies the option verbatim", func(t *testing.T) {
		entry, err := formStateEntry(selectElement("static_select"), "Urgency", "low")
		if err != nil {
			t.Fatal(err)
		}
		opt := entry["selected_option"].(map[string]any)
		if opt["value"] != "opt-low" || getStr(getRec(opt, "text"), "text") != "Low" {
			t.Errorf("selected_option must be the element's option object: %v", opt)
		}
	})

	t.Run("select matches by option value", func(t *testing.T) {
		entry, err := formStateEntry(selectElement("radio_buttons"), "Urgency", "opt-high")
		if err != nil {
			t.Fatal(err)
		}
		if entry["selected_option"].(map[string]any)["value"] != "opt-high" {
			t.Errorf("entry = %v", entry)
		}
	})

	t.Run("unmatched option lists the available labels", func(t *testing.T) {
		_, err := formStateEntry(selectElement("static_select"), "Urgency", "Medium")
		if err == nil || !strings.Contains(err.Error(), "Low, High") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("grouped options are flattened", func(t *testing.T) {
		element := map[string]any{
			"type": "static_select",
			"option_groups": []any{map[string]any{
				"options": []any{map[string]any{
					"text":  map[string]any{"type": "plain_text", "text": "Grouped"},
					"value": "opt-grouped",
				}},
			}},
		}
		entry, err := formStateEntry(element, "Category", "Grouped")
		if err != nil {
			t.Fatal(err)
		}
		if entry["selected_option"].(map[string]any)["value"] != "opt-grouped" {
			t.Errorf("entry = %v", entry)
		}
	})

	t.Run("checkboxes split on commas", func(t *testing.T) {
		entry, err := formStateEntry(selectElement("checkboxes"), "Tags", "Low, opt-high")
		if err != nil {
			t.Fatal(err)
		}
		opts := entry["selected_options"].([]any)
		if len(opts) != 2 {
			t.Fatalf("opts = %v", opts)
		}
	})

	t.Run("datepicker validates the format", func(t *testing.T) {
		entry, err := formStateEntry(map[string]any{"type": "datepicker"}, "Due", "2026-01-31")
		if err != nil || entry["selected_date"] != "2026-01-31" {
			t.Fatalf("entry = %v err = %v", entry, err)
		}
		if _, err := formStateEntry(map[string]any{"type": "datepicker"}, "Due", "31/01/2026"); err == nil {
			t.Fatal("non-ISO date must error")
		}
	})

	t.Run("timepicker validates the format", func(t *testing.T) {
		entry, err := formStateEntry(map[string]any{"type": "timepicker"}, "At", "09:30")
		if err != nil || entry["selected_time"] != "09:30" {
			t.Fatalf("entry = %v err = %v", entry, err)
		}
		if _, err := formStateEntry(map[string]any{"type": "timepicker"}, "At", "9.30pm"); err == nil {
			t.Fatal("non-HH:MM time must error")
		}
	})

	t.Run("unsupported element types error instead of guessing a shape", func(t *testing.T) {
		_, err := formStateEntry(map[string]any{"type": "file_input"}, "Attachment", "x")
		if err == nil || !strings.Contains(err.Error(), "file_input") {
			t.Fatalf("err = %v", err)
		}
	})
}
