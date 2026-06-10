package daemon

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/version"
)

// shortSockPath returns a socket path short enough for the unix sun_path limit
// (t.TempDir() under macOS test runners can exceed it).
func shortSockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ccr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// fakeHolder is an in-process stand-in for an old daemon holding the socket: it
// answers Health with a fixed version and runs onShutdown when asked to step down.
type fakeHolder struct {
	ln         net.Listener
	version    string
	shutdowns  atomic.Int32
	onShutdown func()
}

func startFakeHolder(t *testing.T, socket, ver string) *fakeHolder {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("fake holder listen: %v", err)
	}
	h := &fakeHolder{ln: ln, version: ver}
	h.onShutdown = func() { h.Close() }
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var req Request
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					return
				}
				resp := Response{Proto: ProtocolVersion, OK: true, DaemonVersion: h.version}
				_ = json.NewEncoder(conn).Encode(resp)
				if req.Op == OpShutdown {
					h.shutdowns.Add(1)
					h.onShutdown()
				}
			}(conn)
		}
	}()
	t.Cleanup(h.Close)
	return h
}

func (h *fakeHolder) Close() { _ = h.ln.Close() }

func evictServer(socket string) *Server {
	return &Server{
		socket:       socket,
		log:          log.New(io.Discard, "", 0),
		evictTimeout: 500 * time.Millisecond,
	}
}

func TestListenNoHolderBinds(t *testing.T) {
	sock := shortSockPath(t)
	// A stale socket file with no listener behind it must not block binding.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := evictServer(sock).listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.Close()
}

func TestEvictRefusesSameVersionHolder(t *testing.T) {
	sock := shortSockPath(t)
	startFakeHolder(t, sock, version.String())

	_, err := evictServer(sock).listen()
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want same-version refusal", err)
	}
}

func TestEvictShutsDownSkewedHolder(t *testing.T) {
	sock := shortSockPath(t)
	h := startFakeHolder(t, sock, "v0.0.1-old")

	ln, err := evictServer(sock).listen()
	if err != nil {
		t.Fatalf("listen after eviction: %v", err)
	}
	ln.Close()
	if n := h.shutdowns.Load(); n != 1 {
		t.Fatalf("holder received %d shutdowns, want 1", n)
	}
}

func TestEvictKillsWedgedHolder(t *testing.T) {
	sock := shortSockPath(t)
	h := startFakeHolder(t, sock, "v0.0.1-old")
	h.onShutdown = func() {} // ack the shutdown but keep holding the socket

	const wedgedPID = 424242
	origHolderPID, origKillProc := holderPID, killProc
	t.Cleanup(func() { holderPID, killProc = origHolderPID, origKillProc })
	var killed atomic.Int32
	holderPID = func(*Client) (int, error) { return wedgedPID, nil }
	killProc = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			if pid != wedgedPID {
				t.Errorf("SIGKILL sent to pid %d, want %d", pid, wedgedPID)
			}
			killed.Add(1)
			h.Close() // the "kill" releases the socket
			return nil
		}
		return syscall.ESRCH // the exit-wait probe: process already gone
	}

	ln, err := evictServer(sock).listen()
	if err != nil {
		t.Fatalf("listen after kill: %v", err)
	}
	ln.Close()
	if killed.Load() != 1 {
		t.Fatal("wedged holder was not SIGKILLed")
	}
	if h.shutdowns.Load() != 1 {
		t.Fatal("graceful shutdown was not attempted before the kill")
	}
}

func TestWaitGone(t *testing.T) {
	sock := shortSockPath(t)
	h := startFakeHolder(t, sock, "v")
	c := &Client{socket: sock}

	if c.WaitGone(300 * time.Millisecond) {
		t.Fatal("WaitGone reported a live socket as gone")
	}
	h.Close()
	if !c.WaitGone(2 * time.Second) {
		t.Fatal("WaitGone never saw the socket release")
	}
}

func TestPeerPIDReturnsRealPeer(t *testing.T) {
	sock := shortSockPath(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	pid, err := (&Client{socket: sock}).peerPID()
	if err != nil {
		t.Fatalf("peerPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("peer pid = %d, want self %d", pid, os.Getpid())
	}
}

func TestEnsureCurrentNoopWhenVersionMatches(t *testing.T) {
	sock := shortSockPath(t)
	startFakeHolder(t, sock, version.String())

	// currentVersion against the matching fake: EnsureCurrent's fast path. The
	// spawn arm re-execs the real binary and is covered by manual verification.
	if !currentVersion(&Client{socket: sock}) {
		t.Fatal("currentVersion should match the fake holder's version")
	}
}

func TestKillHolderSparesSelfAndInit(t *testing.T) {
	origHolderPID, origKillProc := holderPID, killProc
	t.Cleanup(func() { holderPID, killProc = origHolderPID, origKillProc })
	killProc = func(pid int, sig syscall.Signal) error {
		t.Errorf("killProc called for pid %d", pid)
		return nil
	}

	for _, pid := range []int{0, 1, os.Getpid()} {
		holderPID = func(*Client) (int, error) { return pid, nil }
		got, err := (&Client{socket: "unused"}).KillHolder()
		if err != nil || got != 0 {
			t.Fatalf("KillHolder(pid=%d) = (%d, %v), want (0, nil)", pid, got, err)
		}
	}
}

func TestKillHolderToleratesESRCH(t *testing.T) {
	origHolderPID, origKillProc := holderPID, killProc
	t.Cleanup(func() { holderPID, killProc = origHolderPID, origKillProc })
	holderPID = func(*Client) (int, error) { return 99999999, nil }
	killProc = func(int, syscall.Signal) error { return syscall.ESRCH }

	pid, err := (&Client{socket: "unused"}).KillHolder()
	if err != nil {
		t.Fatalf("ESRCH must read as already-dead, got %v", err)
	}
	if pid != 99999999 {
		t.Fatalf("pid = %d", pid)
	}
}
