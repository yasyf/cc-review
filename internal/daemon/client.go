package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/procs"
	"github.com/yasyf/cc-review/internal/store"
)

// ErrDaemonUnavailable is returned when the control socket cannot be reached.
var ErrDaemonUnavailable = errors.New("cc-review daemon unavailable")

const dialTimeout = 500 * time.Millisecond

// Client dials the daemon's control socket.
type Client struct {
	socket string
}

// NewClient returns a client for the default socket path.
func NewClient() *Client { return &Client{socket: paths.SocketPath()} }

// Available reports whether the daemon answers on the socket.
func (c *Client) Available() bool {
	conn, err := net.DialTimeout("unix", c.socket, dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// WaitGone polls until the socket stops accepting connections or timeout elapses.
func (c *Client) WaitGone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", c.socket, 200*time.Millisecond)
		if err != nil {
			return true
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// do sends one request and reads one response over a fresh connection.
func (c *Client) do(req Request, timeout time.Duration) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.socket, dialTimeout)
	if err != nil {
		return nil, ErrDaemonUnavailable
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	req.Proto = ProtocolVersion
	req.ClaudePID = procs.ClaudePID()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Health probes liveness and returns the daemon version.
func (c *Client) Health() (*Response, error) {
	return c.do(Request{Op: OpHealth}, 2*time.Second)
}

// Shutdown asks the daemon to step down.
func (c *Client) Shutdown() (*Response, error) {
	return c.do(Request{Op: OpShutdown}, 2*time.Second)
}

// Start snapshots the working tree and resolves/creates the review.
func (c *Client) Start(req Request) (*Response, error) {
	req.Op = OpStart
	return c.do(req, 30*time.Second)
}

// Resolve looks up an existing review for a stream consumer, without creating.
// consumer names the caller (watch | channel) so the daemon can track presence.
func (c *Client) Resolve(session, cwd, consumer string) (*Response, error) {
	return c.do(Request{Op: OpResolve, Session: session, Cwd: cwd, Consumer: consumer}, 10*time.Second)
}

// Reply posts Claude's replies.
func (c *Client) Reply(replies []ReplyInput) (*Response, error) {
	return c.do(Request{Op: OpReply, Replies: replies}, 10*time.Second)
}

// Feedback reads the frozen feedback for a review.
func (c *Client) Feedback(session, cwd string) (*Response, error) {
	return c.do(Request{Op: OpFeedback, Session: session, Cwd: cwd}, 10*time.Second)
}

// Status returns daemon + review status.
func (c *Client) Status(session, cwd string) (*Response, error) {
	return c.do(Request{Op: OpStatus, Session: session, Cwd: cwd}, 5*time.Second)
}

// SessionRecord follows the SessionStart hook's session rotation.
func (c *Client) SessionRecord(session, cwd string) (*Response, error) {
	return c.do(Request{Op: OpSessionRecord, Session: session, Cwd: cwd}, 5*time.Second)
}

// GuardEdit asks whether an edit is permitted for the session's review.
func (c *Client) GuardEdit(session, cwd string) (*Response, error) {
	return c.do(Request{Op: OpGuardEdit, Session: session, Cwd: cwd}, 5*time.Second)
}

// FileStates batch-sets per-file review state. A non-zero aiRequestID ties the
// changes to that request as one undoable unit.
func (c *Client) FileStates(session, cwd string, files []FileStateInput, aiRequestID int64) (*Response, error) {
	return c.do(Request{Op: OpFileStates, Session: session, Cwd: cwd, Files: files, AIRequestID: aiRequestID}, 10*time.Second)
}

// UpdateAIRequest moves an AI request to working, done, or failed. An empty
// summary and nil unmatched keep the stored values.
func (c *Client) UpdateAIRequest(session, cwd string, aiRequestID int64, status, summary string, unmatched []store.Unmatched) (*Response, error) {
	return c.do(Request{
		Op: OpUpdateAIRequest, Session: session, Cwd: cwd,
		AIRequestID: aiRequestID, AIStatus: status, Summary: summary, Unmatched: unmatched,
	}, 10*time.Second)
}

// SubmitOrganization stores the chapter organization for the review's current
// version. versionNumber is required and must match the current version; a
// stale number is rejected with the current one in the error.
func (c *Client) SubmitOrganization(session, cwd string, org store.Organization, versionNumber int) (*Response, error) {
	return c.do(Request{
		Op: OpSubmitOrganization, Session: session, Cwd: cwd,
		Organization: &org, VersionNumber: versionNumber,
	}, 10*time.Second)
}

// ReviewFiles lists the current version's files with their review states.
func (c *Client) ReviewFiles(session, cwd string) (*Response, error) {
	return c.do(Request{Op: OpReviewFiles, Session: session, Cwd: cwd}, 10*time.Second)
}
