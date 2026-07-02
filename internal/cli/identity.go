package cli

// Identity resolution for the per-identity cache namespace: learning a
// credential's Slack (team_id, user_id) — from the stored credential when
// already known, else a one-shot auth.test — and persisting it. Kept apart from
// client construction (context.go) because its best-effort, silent-on-failure
// semantics are a distinct concern.

import (
	"context"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/slack"
)

// resolveIdentityKey returns the <team_id>/<user_id> cache namespace for a
// stored workspace. The ids are persisted (non-secret) once resolved, so steady
// state needs no network: if both are already on the credential we derive the
// key offline. Otherwise we bootstrap a one-shot auth.test, persist via
// SetIdentity, and key from the result. Best-effort and silent: an auth.test
// failure yields "" (caching inert this run, retried next run) rather than
// scoping data to a guessed key. The bootstrap deliberately omits the auth
// refresh — a stale browser token resolves once the real command self-heals it,
// avoiding a duplicate refresh here.
func resolveIdentityKey(store *credential.Store, ws *credential.Workspace, baseOpts []slack.Option, slackAuth slack.Auth) string {
	if ws.TeamID != "" && ws.UserID != "" {
		return slack.IdentityCacheKey(ws.TeamID, ws.UserID)
	}
	teamID, userID := bootstrapIdentity(baseOpts, slackAuth)
	if teamID == "" || userID == "" {
		return ""
	}
	_ = store.SetIdentity(ws.Alias, teamID, userID) // best-effort; keying doesn't depend on the write
	return slack.IdentityCacheKey(teamID, userID)
}

// bootstrapIdentity calls auth.test through a cache-less client to learn the
// team_id/user_id behind a credential. Returns empty ids on any failure —
// silently, since the caller just treats that as "caching off this run".
func bootstrapIdentity(baseOpts []slack.Option, slackAuth slack.Auth) (teamID, userID string) {
	resp, err := slack.New(slackAuth, baseOpts...).API(context.Background(), "auth.test", nil)
	if err != nil {
		return "", ""
	}
	teamID, _ = resp["team_id"].(string)
	userID, _ = resp["user_id"].(string)
	return teamID, userID
}
