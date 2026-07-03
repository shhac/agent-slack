package credential

// noopKeychain is a test double for the file-fallback branch: every operation
// reports "not stored", so the Store keeps secrets in the plaintext file. The
// real no-secret-store platform fallback lives in lib-agent-keyring's
// unavailable backend, which credsKeychain wraps to the same effect.
type noopKeychain struct{}

func (noopKeychain) Get(string) (string, bool) { return "", false }
func (noopKeychain) Set(string, string) bool   { return false }
func (noopKeychain) Delete(string)             {}
func (noopKeychain) Available() bool           { return false }
