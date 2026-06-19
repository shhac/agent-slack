package credential

import "github.com/shhac/lib-agent-cli/creds"

// credsKeychain adapts the shared creds.Keychain to the local Keychain
// interface. The shared type returns errors from Set/Delete; this package's
// Store treats a failed Set as "not stored, fall back to the file", so we
// collapse those errors into the bool/void shapes the interface expects.
type credsKeychain struct {
	kc *creds.Keychain
}

func defaultKeychain() Keychain {
	return &credsKeychain{kc: creds.NewKeychain(keychainService)}
}

func (k *credsKeychain) Get(account string) (string, bool) {
	return k.kc.Get(account)
}

func (k *credsKeychain) Set(account, value string) bool {
	return k.kc.Set(account, value) == nil
}

func (k *credsKeychain) Delete(account string) {
	_ = k.kc.Delete(account)
}

func (k *credsKeychain) Available() bool {
	return k.kc.Available()
}
