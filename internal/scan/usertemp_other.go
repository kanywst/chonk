//go:build !darwin

package scan

// UserTemp is macOS-only: no other system keeps a per-user container of
// caches and app clones outside the home directory.
func UserTemp() string { return "" }
