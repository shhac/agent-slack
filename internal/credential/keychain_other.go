//go:build !darwin

package credential

func defaultKeychain() Keychain { return noopKeychain{} }
