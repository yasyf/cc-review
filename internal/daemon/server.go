package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/yasyf/cc-review/internal/httpapi"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/procs"
	"github.com/yasyf/cc-review/internal/session"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/version"
)

// handleTimeout bounds a single control RPC. It is generous because `start`
// shells out to git to snapshot the tree; every other op is sub-second.
const handleTimeout = 35 * time.Second

// defaultEvictTimeout bounds each phase of evicting a version-skewed holder:
// the graceful-shutdown wait, the post-SIGKILL wait, and the process-exit wait.
const defaultEvictTimeout = 5 * time.Second

// attachGrace is how recently a pid-less review's last SSE attachment must
// have dropped for held to still consider the review occupied.
const attachGrace = 10 * time.Second

// Server is the running daemon: the control-plane unix-socket server plus the
// data/UI HTTP plane it boots.
type Server struct {
	store    *store.Store
	bus      *Bus
	activity *Activity
	resolver session.Resolver
	alive    func(pid int) bool
	socket   string
	log      *log.Logger

	startedAt time.Time

	fixedPort    int // 0 = ephemeral; a fixed dev port lets the Vite proxy find us
	httpPort     int
	evictTimeout time.Duration

	triggerShutdown context.CancelFunc
	wg              sync.WaitGroup
}

// Run is the entry point for `cc-review daemon`. fixedPort pins the HTTP plane to
// a known port for the Vite dev proxy; 0 binds an ephemeral port. It blocks until
// signalled or asked to shut down.
func Run(ctx context.Context, fixedPort int) error {
	if err := paths.EnsureStateDir(); err != nil {
		return err
	}
	st, err := store.Open(paths.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	s := &Server{
		store:        st,
		bus:          NewBus(),
		activity:     NewActivity(),
		alive:        procs.LiveClaude,
		socket:       paths.SocketPath(),
		log:          log.New(os.Stderr, "[cc-review] ", log.LstdFlags),
		startedAt:    time.Now(),
		fixedPort:    fixedPort,
		evictTimeout: defaultEvictTimeout,
	}
	s.resolver = session.Resolver{Store: st, Held: s.held}
	return s.serve(ctx)
}

func (s *Server) serve(parent context.Context) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s.triggerShutdown = stop

	// Bind (and evict any skewed holder of) the control socket before publishing
	// the HTTP handshake: the evicted daemon deletes http.json on its way out, so
	// writing ours first would get clobbered. Connections queue in the listener
	// backlog until the accept loop starts below, so nothing observes the gap.
	ln, err := s.listen()
	if err != nil {
		return err
	}
	var once sync.Once
	closeListener := func() { once.Do(func() { _ = ln.Close() }) }
	defer closeListener()

	if err := s.startHTTP(ctx); err != nil {
		return err
	}

	s.log.Printf("daemon %s started; socket=%s http=127.0.0.1:%d", version.String(), s.socket, s.httpPort)

	go func() {
		<-ctx.Done()
		closeListener()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			s.log.Printf("accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, conn)
		}()
	}

	s.wg.Wait()
	_ = os.Remove(paths.HTTPInfoPath())
	s.log.Printf("daemon stopped")
	return nil
}

// listen binds the control socket, first evicting any version-skewed daemon
// holding it. A stale socket left by a crashed daemon is removed before
// binding; the lazy-start flock prevents two live daemons from racing here.
func (s *Server) listen() (net.Listener, error) {
	if err := s.evictHolder(); err != nil {
		return nil, err
	}
	_ = os.Remove(s.socket)
	if err := os.MkdirAll(filepath.Dir(s.socket), 0o700); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(s.socket, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// evictHolder clears a version-skewed daemon holding the socket: ask it to step
// down, then SIGKILL the exact socket peer if it wedges. A same-version holder
// is never evicted — that is a legitimate running daemon, so refusing here is
// what prevents two daemons from ever evicting each other.
func (s *Server) evictHolder() error {
	c := &Client{socket: s.socket}
	resp, err := c.Health()
	if err != nil {
		return nil // no live holder; a stale socket file is removed by listen
	}
	if resp.DaemonVersion == version.String() {
		return errors.New("another cc-review daemon at the same version is already running")
	}
	s.log.Printf("evicting version-skewed daemon (%s) holding the socket", resp.DaemonVersion)
	pid, _ := c.peerPID() // grab before shutdown: the peer is gone afterwards
	if _, err := c.Shutdown(); err != nil {
		return fmt.Errorf("evict holder %s: %w", resp.DaemonVersion, err)
	}
	if !c.WaitGone(s.evictTimeout) {
		if _, err := c.KillHolder(); err != nil {
			s.log.Printf("kill holder: %v", err)
		}
		if !c.WaitGone(s.evictTimeout) {
			return fmt.Errorf("holder %s did not release the socket within %s", resp.DaemonVersion, s.evictTimeout)
		}
	}
	// The old daemon deletes http.json on its way out, up to its drain window
	// after the socket closes — shipped code we cannot patch. Wait for the
	// process itself to exit so our handshake is not clobbered.
	if pid > 1 && pid != os.Getpid() {
		deadline := time.Now().Add(s.evictTimeout)
		for time.Now().Before(deadline) {
			if err := killProc(pid, syscall.Signal(0)); errors.Is(err, syscall.ESRCH) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		s.log.Printf("holder pid %d still exiting; the handshake may be rewritten once", pid)
	}
	return nil
}

// startHTTP binds the data/UI plane on an ephemeral 127.0.0.1 port, publishes the
// port handshake, and serves until ctx is cancelled. Request contexts derive
// from ctx (BaseContext), so cancelling it ends every parked SSE handler
// before the graceful Shutdown drains them — and before Run closes the store.
func (s *Server) startHTTP(ctx context.Context) error {
	addr := "127.0.0.1:0"
	if s.fixedPort != 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", s.fixedPort)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.httpPort = ln.Addr().(*net.TCPAddr).Port
	if err := writeHTTPInfo(HTTPInfo{Port: s.httpPort}); err != nil {
		ln.Close()
		return err
	}
	api := httpapi.New(s.store, s)
	srv := &http.Server{
		Handler:     api.Handler(),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Printf("http serve: %v", err)
		}
	}()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	return nil
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(handleTimeout))
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResp(conn, Response{OK: false, Error: "bad request: " + err.Error()})
		return
	}
	writeResp(conn, s.dispatch(ctx, req))
}

// held reports whether the window owning a review is still alive: a pid-bound
// review by process liveness, a pid-less one by a recent SSE attachment. It
// runs only on the start/session-record paths, never per resolve poll and
// never inside a store transaction.
func (s *Server) held(_ context.Context, r store.Review) bool {
	if r.ClaudePID != 0 {
		return s.alive(r.ClaudePID)
	}
	return s.activity.AttachedWithin(r.ID, attachGrace)
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	// Health and shutdown answer regardless of protocol version: cross-version
	// eviction probes (evictHolder) depend on both.
	switch req.Op {
	case OpHealth:
		return Response{OK: true, DaemonVersion: version.String()}
	case OpShutdown:
		s.triggerShutdown()
		return Response{OK: true}
	}
	if req.Proto != ProtocolVersion {
		return errResp(fmt.Sprintf(
			"cc-review protocol skew: daemon speaks v%d, request is v%d — client and daemon versions differ; retry the command (first contact upgrades the daemon)",
			ProtocolVersion, req.Proto))
	}
	switch req.Op {
	case OpStart:
		return s.handleStart(ctx, req)
	case OpResolve:
		return s.handleResolve(ctx, req)
	case OpReply:
		return s.handleReply(ctx, req)
	case OpFeedback:
		return s.handleFeedback(ctx, req)
	case OpStatus:
		return s.handleStatus(ctx, req)
	case OpSessionRecord:
		return s.handleSessionRecord(ctx, req)
	case OpGuardEdit:
		return s.handleGuardEdit(ctx, req)
	default:
		return Response{OK: false, Error: "unknown op: " + string(req.Op)}
	}
}

func writeResp(conn net.Conn, r Response) {
	r.Proto = ProtocolVersion
	_ = json.NewEncoder(conn).Encode(r)
}
