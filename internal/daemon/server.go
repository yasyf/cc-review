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

	"github.com/yasyf/cc-review/internal/decisions"
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
	store     *store.Store
	decisions *decisions.Log
	bus       *Bus
	activity  *Activity
	resolver  session.Resolver
	alive     func(pid int) bool
	socket    string
	log       *log.Logger
	sliceWarn sync.Once

	fixedPort    int // 0 = ephemeral; a fixed dev port lets the Vite proxy find us
	httpPort     int
	evictTimeout time.Duration

	repoMu    sync.Mutex
	repoLocks map[string]*sync.Mutex

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
	ledger, err := decisions.Open(decisionsPath())
	if err != nil {
		return err
	}
	defer ledger.Close()

	s := &Server{
		store:        st,
		decisions:    ledger,
		bus:          NewBus(),
		activity:     NewActivity(),
		alive:        procs.LiveClaude,
		socket:       paths.SocketPath(),
		log:          log.New(os.Stderr, "[cc-review] ", log.LstdFlags),
		fixedPort:    fixedPort,
		evictTimeout: defaultEvictTimeout,
		repoLocks:    make(map[string]*sync.Mutex),
	}
	s.resolver = session.Resolver{Store: st, Held: s.held}
	return s.serve(ctx)
}

// decisionsPath is the family decision ledger location; CC_DECISIONS_DB
// overrides it for tests.
func decisionsPath() string {
	if p := os.Getenv("CC_DECISIONS_DB"); p != "" {
		return p
	}
	return decisions.DefaultPath()
}

func (s *Server) serve(parent context.Context) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s.triggerShutdown = stop

	// Bind (and evict any older holder of) the control socket before publishing
	// the HTTP handshake: pre-fix daemons delete http.json on their way out, so
	// writing ours first would get clobbered; post-fix daemons leave the file
	// for the successor to reuse its port. Connections queue in the listener
	// backlog until the accept loop starts below, so nothing observes the gap.
	ln, err := s.listen()
	if err != nil {
		return err
	}
	var once sync.Once
	closeListener := func() { once.Do(func() { _ = ln.Close() }) }
	defer closeListener()

	if err := s.reconcileChannelEvents(ctx); err != nil {
		return err
	}
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
	s.log.Printf("daemon stopped")
	return nil
}

// listen binds the control socket, first evicting any strictly older daemon
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

// evictHolder clears a strictly older daemon holding the socket: ask it to
// step down, then SIGKILL the exact socket peer if it wedges. A same-or-newer
// holder is never evicted — refusing the tie is what prevents two daemons from
// ever evicting each other, and refusing a newer holder makes a spawned older
// daemon exit while its spawning client converges on the newer holder instead
// of ping-ponging evictions.
func (s *Server) evictHolder() error {
	c := &Client{socket: s.socket}
	resp, err := c.Health()
	if err != nil {
		return nil // no live holder; a stale socket file is removed by listen
	}
	if !version.Newer(version.String(), resp.DaemonVersion) {
		return fmt.Errorf("cc-review daemon %s already holds the socket (this binary is %s)", resp.DaemonVersion, version.String())
	}
	s.log.Printf("evicting older daemon (%s) holding the socket", resp.DaemonVersion)
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
	// Pre-fix daemons delete http.json on their way out, up to their drain
	// window after the socket closes — shipped code we cannot patch. Wait for
	// the process itself to exit so our handshake is not clobbered; the wait is
	// also what lets listenHTTP read the predecessor's port and reuse it.
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

// listenHTTP binds the data/UI plane. A fixed dev port binds exactly or fails
// loud; otherwise the port last published to http.json is tried first so
// printed review URLs survive a daemon swap, falling back to an ephemeral
// port. The prior port is a reuse hint, not a contract.
func (s *Server) listenHTTP() (net.Listener, error) {
	if s.fixedPort != 0 {
		return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.fixedPort))
	}
	if prev := readHTTPInfo().Port; prev != 0 {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", prev)); err == nil {
			return ln, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// startHTTP binds the data/UI plane on 127.0.0.1, publishes the port
// handshake, and serves until ctx is cancelled. Request contexts derive
// from ctx (BaseContext), so cancelling it ends every parked SSE handler
// before the graceful Shutdown drains them — and before Run closes the store.
func (s *Server) startHTTP(ctx context.Context) error {
	ln, err := s.listenHTTP()
	if err != nil {
		return err
	}
	s.httpPort = ln.Addr().(*net.TCPAddr).Port
	if err := writeHTTPInfo(HTTPInfo{Port: s.httpPort}); err != nil {
		ln.Close()
		return err
	}
	api := httpapi.New(s.store, s.decisions, s.log, s)
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

// repoLock returns the mutex serializing working-tree snapshots for one repo,
// so a turn boundary and a review capture never describe interleaved trees.
func (s *Server) repoLock(repoRoot string) *sync.Mutex {
	s.repoMu.Lock()
	defer s.repoMu.Unlock()
	mu, ok := s.repoLocks[repoRoot]
	if !ok {
		mu = &sync.Mutex{}
		s.repoLocks[repoRoot] = mu
	}
	return mu
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
			"cc-review protocol skew: daemon speaks v%d, request is v%d — this session is pinned to an older plugin version; restart the session to pick up the current one",
			ProtocolVersion, req.Proto))
	}
	switch req.Op {
	case OpStart:
		return s.handleStart(ctx, req)
	case OpResolve:
		return s.handleResolve(ctx, req)
	case OpChannelAck:
		return s.handleChannelAck(ctx, req)
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
	case OpFileStates:
		return s.handleFileStates(ctx, req)
	case OpUpdateAIRequest:
		return s.handleUpdateAIRequest(ctx, req)
	case OpSubmitOrganization:
		return s.handleSubmitOrganization(ctx, req)
	case OpReviewFiles:
		return s.handleReviewFiles(ctx, req)
	case OpTurnStart:
		return s.handleTurnStart(ctx, req)
	case OpTurnEnd:
		return s.handleTurnEnd(ctx, req)
	default:
		return Response{OK: false, Error: "unknown op: " + string(req.Op)}
	}
}

func writeResp(conn net.Conn, r Response) {
	r.Proto = ProtocolVersion
	_ = json.NewEncoder(conn).Encode(r)
}
