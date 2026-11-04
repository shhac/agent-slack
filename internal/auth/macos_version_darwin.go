package auth

import "golang.org/x/sys/unix"

// macOSMajorVersion reports the running macOS major version (e.g. 15), or 0
// when it can't be determined.
func macOSMajorVersion() int {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return 0
	}
	return parseMajorVersion(v)
}
