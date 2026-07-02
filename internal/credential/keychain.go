package credential

import "sync"

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

// MemoryKeychain is an in-memory Keychain for tests, safe for concurrent use
// so it can back concurrency tests under -race. The zero value is not usable;
// use NewMemoryKeychain.
type MemoryKeychain struct {
	mu      sync.Mutex
	entries map[string]string
}

func NewMemoryKeychain() *MemoryKeychain {
	return &MemoryKeychain{entries: map[string]string{}}
}

func (m *MemoryKeychain) Get(account string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.entries[account]
	return v, ok
}

func (m *MemoryKeychain) Set(account, value string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[account] = value
	return true
}

func (m *MemoryKeychain) Delete(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, account)
}

func (m *MemoryKeychain) Available() bool { return true }

// snapshot returns a copy of the stored entries for test assertions.
func (m *MemoryKeychain) snapshot() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.entries))
	for k, v := range m.entries {
		out[k] = v
	}
	return out
}

// noopKeychain is a test double for the file-fallback branch: every operation
// reports "not stored", so the Store keeps secrets in the plaintext file. The
// real no-secret-store platform fallback lives in lib-agent-keyring's
// unavailable backend, which credsKeychain wraps to the same effect.
type noopKeychain struct{}

func (noopKeychain) Get(string) (string, bool) { return "", false }
func (noopKeychain) Set(string, string) bool   { return false }
func (noopKeychain) Delete(string)             {}
func (noopKeychain) Available() bool           { return false }

func isPlaceholder(v string) bool { return v == "" || v == keychainPlaceholder }

// Keychain accounts are keyed by workspace alias (store version 2): several
// aliases may hold credentials for the same workspace URL, each — including
// the browser d cookie — with its own entry.
func xoxcAccount(alias string) string  { return "xoxc:" + alias }
func tokenAccount(alias string) string { return "token:" + alias }
func xoxdAccount(alias string) string  { return "xoxd:" + alias }

// Version-1 accounts were keyed by normalized URL, with one shared xoxd
// cookie across all browser workspaces. Read (and deleted) only by migration.
func legacyXoxcAccount(normalizedURL string) string  { return "xoxc:" + normalizedURL }
func legacyTokenAccount(normalizedURL string) string { return "token:" + normalizedURL }

const legacyXoxdAccount = "xoxd"
