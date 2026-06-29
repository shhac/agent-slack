package credential

// keychainService is the macOS Keychain service name for all agent-slack
// secrets. It follows the agent-* family reverse-DNS convention (cf. lin's
// "app.paulie.lin").
const keychainService = "app.paulie.agent-slack"

// MCPKeychainService is the Keychain service for the MCP server's local-OAuth
// secrets (signing key, pairing code, client registrations, tokens). It is the
// CLI's service plus a ".mcp" namespace — keeping the OAuth trust axis separate
// from the API credentials while staying within the family reverse-DNS
// convention (so it's "app.paulie.agent-slack.mcp", not a bare "agent-slack.mcp").
func MCPKeychainService() string { return keychainService + ".mcp" }

// keychainPlaceholder is written to the on-disk credentials file in place of a
// secret that has been stored in the Keychain instead.
const keychainPlaceholder = "__KEYCHAIN__"

// Keychain is the minimal secret store the credential Store depends on. The
// real macOS implementation shells out to the `security` CLI; tests inject an
// in-memory implementation so they never touch the user's real Keychain.
type Keychain interface {
	// Get returns the stored secret for account and whether it was found.
	Get(account string) (string, bool)
	// Set stores value for account, returning true on success. A false return
	// (e.g. non-macOS, or the CLI failing) signals the caller to fall back to
	// writing the secret to the plaintext file.
	Set(account, value string) bool
	// Delete removes the entry for account. Missing entries are not an error.
	Delete(account string)
	// Available reports whether this Keychain can actually persist secrets.
	Available() bool
}

// MemoryKeychain is an in-memory Keychain for tests. The zero value is not
// usable; use NewMemoryKeychain.
type MemoryKeychain struct {
	entries map[string]string
}

func NewMemoryKeychain() *MemoryKeychain {
	return &MemoryKeychain{entries: map[string]string{}}
}

func (m *MemoryKeychain) Get(account string) (string, bool) {
	v, ok := m.entries[account]
	return v, ok
}

func (m *MemoryKeychain) Set(account, value string) bool {
	m.entries[account] = value
	return true
}

func (m *MemoryKeychain) Delete(account string) {
	delete(m.entries, account)
}

func (m *MemoryKeychain) Available() bool { return true }

// noopKeychain is used on platforms without a supported secret store. Every
// operation reports "not stored", which makes the Store fall back to the
// plaintext file.
type noopKeychain struct{}

func (noopKeychain) Get(string) (string, bool) { return "", false }
func (noopKeychain) Set(string, string) bool   { return false }
func (noopKeychain) Delete(string)             {}
func (noopKeychain) Available() bool           { return false }
