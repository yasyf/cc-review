// Package testhome pins a test's home directory to a scratch directory.
package testhome

import "testing"

// Pin points HOME and DAEMONKIT_HOME at dir for the duration of the test.
// daemonkit resolves the home directory through the passwd database and ignores
// HOME, so setting HOME alone lets state writes escape into the real home.
func Pin(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("DAEMONKIT_HOME", dir)
}

// Temp pins the test's home to a fresh t.TempDir and returns it.
func Temp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	Pin(t, dir)
	return dir
}
