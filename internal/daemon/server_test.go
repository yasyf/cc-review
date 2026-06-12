package daemon

import (
	"net"
	"os"
	"testing"

	"github.com/yasyf/cc-review/internal/paths"
)

// isolateStateDir points ~/.cc-review at a fresh temp dir (os.UserHomeDir
// honors $HOME) so each case starts without an http.json handshake.
func isolateStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := paths.EnsureStateDir(); err != nil {
		t.Fatal(err)
	}
}

func boundPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestListenHTTPPortReuse(t *testing.T) {
	t.Run("no handshake binds ephemeral", func(t *testing.T) {
		isolateStateDir(t)

		ln, err := (&Server{}).listenHTTP()
		if err != nil {
			t.Fatalf("listenHTTP: %v", err)
		}
		defer ln.Close()
		if boundPort(t, ln) == 0 {
			t.Fatal("ephemeral bind returned port 0")
		}
	})

	t.Run("free published port is reused", func(t *testing.T) {
		isolateStateDir(t)
		prev, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := boundPort(t, prev)
		prev.Close()
		if err := writeHTTPInfo(HTTPInfo{Port: port}); err != nil {
			t.Fatal(err)
		}

		ln, err := (&Server{}).listenHTTP()
		if err != nil {
			t.Fatalf("listenHTTP: %v", err)
		}
		defer ln.Close()
		if got := boundPort(t, ln); got != port {
			t.Fatalf("bound port %d, want published port %d", got, port)
		}
	})

	t.Run("held published port falls back to ephemeral", func(t *testing.T) {
		isolateStateDir(t)
		holder, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		port := boundPort(t, holder)
		if err := writeHTTPInfo(HTTPInfo{Port: port}); err != nil {
			t.Fatal(err)
		}

		ln, err := (&Server{}).listenHTTP()
		if err != nil {
			t.Fatalf("listenHTTP: %v", err)
		}
		defer ln.Close()
		if got := boundPort(t, ln); got == port {
			t.Fatalf("bound the held port %d, want a different one", port)
		}
	})

	t.Run("occupied fixed port fails", func(t *testing.T) {
		isolateStateDir(t)
		holder, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()

		if _, err := (&Server{fixedPort: boundPort(t, holder)}).listenHTTP(); err == nil {
			t.Fatal("listenHTTP on an occupied fixed port must fail")
		}
	})

	t.Run("corrupt handshake binds ephemeral", func(t *testing.T) {
		isolateStateDir(t)
		if err := os.WriteFile(paths.HTTPInfoPath(), []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}

		ln, err := (&Server{}).listenHTTP()
		if err != nil {
			t.Fatalf("listenHTTP: %v", err)
		}
		defer ln.Close()
		if boundPort(t, ln) == 0 {
			t.Fatal("ephemeral bind returned port 0")
		}
	})
}
