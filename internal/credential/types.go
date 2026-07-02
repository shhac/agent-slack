// The credential data model: auth shapes, workspaces, the on-disk file
// format, and the errors resolution/upsert can return.
package credential

import (
	"errors"
	"fmt"
	"strings"
)

type AuthType string

const (
	AuthBrowser  AuthType = "browser"
	AuthStandard AuthType = "standard"
)

// Auth holds the secrets for one workspace. Exactly one shape is valid per
// Type: browser uses XOXC+XOXD, standard uses Token.
type Auth struct {
	Type  AuthType `json:"auth_type"`
	Token string   `json:"token,omitempty"`
	XOXC  string   `json:"xoxc_token,omitempty"`
	XOXD  string   `json:"xoxd_cookie,omitempty"`
}

// Workspace is one named credential set. Alias is the unique key; several
// aliases may share a URL (two humans in the same Slack workspace).
type Workspace struct {
	Alias      string `json:"alias"`
	URL        string `json:"workspace_url"`
	Name       string `json:"workspace_name,omitempty"`
	TeamID     string `json:"team_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	TeamDomain string `json:"team_domain,omitempty"`
	Auth       Auth   `json:"auth"`
}

type Credentials struct {
	Version          int         `json:"version"`
	UpdatedAt        string      `json:"updated_at,omitempty"`
	DefaultWorkspace string      `json:"default_workspace,omitempty"`
	Workspaces       []Workspace `json:"workspaces"`
}

// storeVersion is the current credentials-file format: alias-keyed
// workspaces with per-alias Keychain accounts. See
// design-docs/workspace-aliases.md.
const storeVersion = 2

// credentialsFile is the on-disk shape across versions: current fields plus
// the version-1 default field, which migration maps onto an alias.
type credentialsFile struct {
	Credentials
	LegacyDefaultURL string `json:"default_workspace_url,omitempty"`
}

// ErrWorkspaceNotFound is returned when no stored workspace matches a request.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// AmbiguousSelectorError is returned when a --workspace selector matches more
// than one stored workspace.
type AmbiguousSelectorError struct {
	Selector string
	Matches  []string
}

func (e *AmbiguousSelectorError) Error() string {
	return fmt.Sprintf("workspace selector %q is ambiguous; matches: %s", e.Selector, strings.Join(e.Matches, ", "))
}

// AmbiguousURLError is returned when an alias-less upsert (an import) hits a
// URL that several stored aliases share — guessing which entry to overwrite
// would be a cross-user credential write.
type AmbiguousURLError struct {
	URL     string
	Aliases []string
}

func (e *AmbiguousURLError) Error() string {
	return fmt.Sprintf("several stored workspaces use %s (%s); pass an explicit alias", e.URL, strings.Join(e.Aliases, ", "))
}
