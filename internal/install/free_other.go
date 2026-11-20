//go:build !linux && !darwin

package install

// freeBytes is unknown off Linux and macOS, where preflight stops anyway.
func freeBytes(string) (uint64, bool) { return 0, false }
