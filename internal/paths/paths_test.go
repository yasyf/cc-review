package paths

import (
	"path/filepath"
	"testing"
)

func TestV1NamespaceIgnoresPreEpochState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".cc-review", "v1")
	if got := StateDir(); got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}
	if got := DBPath(); got != filepath.Join(want, "cc-interact-v1", "state.db") {
		t.Fatalf("DBPath = %q, want exact cc-interact v1 namespace", got)
	}
	if got := App().SocketPath(); got != filepath.Join(want, "daemon.sock") {
		t.Fatalf("SocketPath = %q, want v1 namespace", got)
	}
	if DBPath() == filepath.Join(home, ".cc-review", "state.db") {
		t.Fatal("v1 namespace reused pre-epoch state.db")
	}
}
