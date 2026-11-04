package auth

import "testing"

func TestMacOSMajorVersionReadsRunningSystem(t *testing.T) {
	if got := macOSMajorVersion(); got < 10 {
		t.Fatalf("macOSMajorVersion() = %d, want a real macOS major version", got)
	}
}
