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
