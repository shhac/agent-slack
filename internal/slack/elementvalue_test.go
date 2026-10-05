package slack

import (
	"strings"
	"testing"
)

// One value builder serves three callers, and each must keep its own words:
// a workflow run was abandoned, an app form was not submitted, a menu was not
// pressed. Sharing the builder once rewrote the workflow hints silently.
func TestElementValueErrorsKeepTheCallersWording(t *testing.T) {
	cases := []struct {
		vc       valueContext
		wantMsg  string
		wantHint string
	}{
		{workflowFieldValues, `field "Due"`, abandonedRunHint},
		{appFormValues, `field "Due"`, nothingSubmittedHint},
		{menuValues, `menu "Due"`, "nothing was pressed"},
	}
	for _, tc := range cases {
		_, err := formStateEntry(map[string]any{"type": "datepicker"}, "Due", "31/01/2026", tc.vc)
		hint := agentHint(t, err)
		if !strings.Contains(err.Error(), tc.wantMsg) || !strings.HasSuffix(hint, tc.wantHint) {
			t.Errorf("%s: err %q hint %q, want %q / …%q", tc.vc.noun, err, hint, tc.wantMsg, tc.wantHint)
		}
	}
}

func TestFormStateEntryMenusAndIDs(t *testing.T) {
	options := []any{
		map[string]any{"text": map[string]any{"type": "plain_text", "text": "Retry"}, "value": "retry"},
		map[string]any{"text": map[string]any{"type": "plain_text", "text": "Cancel"}, "value": "cancel"},
	}
	overflow, err := formStateEntry(map[string]any{"type": "overflow", "options": options}, "More", "retry", menuValues)
	if err != nil || overflow["selected_option"].(map[string]any)["value"] != "retry" {
		t.Errorf("overflow = %v, %v", overflow, err)
	}
	multi, err := formStateEntry(map[string]any{"type": "multi_static_select", "options": options}, "Steps", "Retry, cancel", menuValues)
	if err != nil || len(multi["selected_options"].([]any)) != 2 {
		t.Errorf("multi_static_select = %v, %v", multi, err)
	}

	user, err := formStateEntry(map[string]any{"type": "users_select"}, "Owner", "U0000000001", appFormValues)
	if err != nil || user["selected_user"] != "U0000000001" {
		t.Errorf("users_select = %v, %v", user, err)
	}
	users, err := formStateEntry(map[string]any{"type": "multi_users_select"}, "Owners", "U1, U2", appFormValues)
	if err != nil || len(users["selected_users"].([]any)) != 2 {
		t.Errorf("multi_users_select = %v, %v", users, err)
	}
	for _, bad := range []string{"U1,U2", " , "} {
		if _, err := formStateEntry(map[string]any{"type": "users_select"}, "Owner", bad, appFormValues); err == nil {
			t.Errorf("users_select accepted %q", bad)
		}
	}
}
