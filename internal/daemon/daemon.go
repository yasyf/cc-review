package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	ccd "github.com/yasyf/cc-interact/daemon"
	ccevent "github.com/yasyf/cc-interact/event"
	"github.com/yasyf/cc-interact/subject"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/digest"
	"github.com/yasyf/cc-review/internal/httpapi"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/procs"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/version"
	"github.com/yasyf/cc-review/internal/web"
	"github.com/yasyf/cc-review/internal/wire"
)

const (
	statusOpen = "open"

	// organizePrompt seeds the system AI request handleStart creates per version,
	// asking the live Claude session to chapter the diff.
	organizePrompt = "Organize this review into chapters and rate per-file risk."

	// channelConsumer is the stream-consumer name the channel server registers
	// under; channelState keys presence to it.
	channelConsumer = "channel"

	// channelPollWindow is how recent a channel resolve poll must be to count as
	// presence; it only distinguishes pending from inactive.
	channelPollWindow = 3 * time.Second

	// stalePendingTTL is how long a human AI-bar request may sit pending before
	// the sweeper fails it.
	stalePendingTTL = 10 * time.Minute

	// sweepInterval is how often the daemon sweeps for stale pending requests.
	sweepInterval = 1 * time.Minute

	gateBlockReason = "cc-review: an open review is awaiting your feedback — edits are blocked until you press Submit in the browser."
	gateErrorReason = "cc-review: could not read review status; blocking the edit to be safe. Try `cc-review status`, or `cc-review stop` to clear the daemon."
)

// lifecycle names the subject statuses the resolver writes: a fresh review is
// born open; a fresh start closes the window's prior review.
var lifecycle = subject.Lifecycle{Initial: statusOpen, Closed: "closed"}

// review holds the cross-handler state the substrate's HandlerCtx does not carry:
// the shared decision ledger and the daemon logger.
type review struct {
	decisions *decisions.Log
	log       *log.Logger
	sliceWarn sync.Once
}

// Serve builds the cc-interact daemon for the review domain — scope = repo root,
// gate = block while a review is open, version-stamped channel presence — wires
// the review ops and the REST plane onto it, starts the stale-request sweeper,
// and serves until the context is cancelled. fixedPort pins the HTTP plane for
// the Vite dev proxy; 0 binds an ephemeral port.
func Serve(ctx context.Context, fixedPort int) error {
	if err := paths.EnsureStateDir(); err != nil {
		return err
	}
	ledger, err := decisions.Open(decisionsPath())
	if err != nil {
		return err
	}
	defer ledger.Close()
	rv := &review{decisions: ledger, log: log.New(os.Stderr, "[cc-review] ", log.LstdFlags)}

	s, err := ccd.New(ccd.Config{
		AppName:           "cc-review",
		Paths:             paths.App(),
		Version:           version.String(),
		ActiveStatuses:    []string{statusOpen},
		WindowAlive:       procs.LiveClaude,
		ScopeResolve:      vcs.Root,
		Gate:              rv.gate,
		GateErrorReason:   gateErrorReason,
		GateObserve:       rv.gateObserve,
		OnPresenceChange:  rv.onPresenceChange,
		PresenceEventType: store.EventChannelChanged,
		BootReconcile:     rv.bootReconcile,
		Migrate:           store.ReviewMigrate,
		FixedPort:         fixedPort,
	})
	if err != nil {
		return err
	}
	s.Register(OpStart, rv.handleStart)
	s.Register(OpReply, rv.handleReply)
	s.Register(OpFeedback, rv.handleFeedback)
	s.Register(OpFileStates, rv.handleFileStates)
	s.Register(OpFileStatesByRisk, rv.handleFileStatesByRisk)
	s.Register(OpUpdateAIRequest, rv.handleUpdateAIRequest)
	s.Register(OpSubmitOrganization, rv.handleSubmitOrganization)
	s.Register(OpReviewFiles, rv.handleReviewFiles)
	s.Register(OpAnnotate, rv.handleAnnotate)
	s.Register(OpTurnStart, rv.handleTurnStart)
	s.Register(OpTurnEnd, rv.handleTurnEnd)

	httpapi.RESTMount(s.Mux(), httpapi.Deps{
		Store:             store.New(s.DB()),
		Decisions:         ledger,
		Log:               rv.log,
		Append:            s.Append,
		ConsumerConnected: s.ConsumerConnected,
		Dist:              web.Dist(),
	})

	go rv.sweepLoop(ctx, s)
	return s.Serve(ctx)
}

// decisionsPath is the family decision ledger location; CC_DECISIONS_DB
// overrides it for tests.
func decisionsPath() string {
	if p := os.Getenv("CC_DECISIONS_DB"); p != "" {
		return p
	}
	return decisions.DefaultPath()
}

// gate blocks edits while the review is open; a non-open (submitted/closed)
// review permits them.
func (rv *review) gate(_ context.Context, sub subject.Subject, _ ccd.ToolCall) (bool, string) {
	if sub.Status == statusOpen {
		return false, gateBlockReason
	}
	return true, ""
}

// gateObserve ledgers a resolved gate verdict in decisions_v1. A deliberate
// deviation from fail-fast: the verdict is the gate's job, the ledger row is
// telemetry — digest or append failures log and never affect the response.
func (rv *review) gateObserve(_ context.Context, sub subject.Subject, tool ccd.ToolCall, allow bool, reason string) {
	action := "allow"
	if !allow {
		action = "block"
	}
	toolDigest, err := digest.Tool(tool.Name, tool.Input)
	if err != nil {
		toolDigest = ""
		rv.log.Printf("gate decision: digest: %v", err)
	}
	detail := map[string]any{"review_id": sub.ID}
	if fp := toolInputFilePath(tool.Input); fp != "" {
		detail["file_path"] = fp
	}
	detailJSON, _ := json.Marshal(detail)
	if err := rv.decisions.Append(decisions.Decision{
		TsMs: time.Now().UnixMilli(), SessionID: sub.SessionID, Source: "cc-review", Kind: "gate",
		Event: "PreToolUse", Action: action, ToolName: tool.Name, ToolDigest: toolDigest,
		Message: reason, DetailJSON: string(detailJSON),
	}); err != nil {
		rv.log.Printf("gate decision: append: %v", err)
	}
}

// toolInputFilePath pulls the guarded file out of a raw tool input: file_path
// for Edit/Write, notebook_path for NotebookEdit.
func toolInputFilePath(input json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(input, &in)
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.NotebookPath
}

// onPresenceChange emits a version-stamped channel.changed event when a named
// consumer's connectivity to a review flips.
func (rv *review) onPresenceChange(ctx context.Context, s *ccd.Server, subjectID string, connected bool) {
	st := store.New(s.DB())
	v, ok, err := st.LatestVersion(ctx, subjectID)
	if err != nil || !ok {
		return
	}
	emit(ctx, s.Append, subjectID, ccevent.OriginSystem, store.EventChannelChanged, v.VersionNumber,
		map[string]any{"connected": connected})
}

// bootReconcile closes out channel.changed state orphaned by a daemon death: the
// SSE detach defer and debounce timer die with the process, so a log whose last
// word is connected:true would replay as a live channel forever. It runs once at
// boot, before the HTTP plane accepts attaches, so every stale connected:true is
// provably false.
func (rv *review) bootReconcile(ctx context.Context, s *ccd.Server) error {
	st := store.New(s.DB())
	ids, err := st.StaleConnectedReviews(ctx)
	if err != nil {
		return fmt.Errorf("reconcile channel events: %w", err)
	}
	for _, id := range ids {
		v, ok, err := st.LatestVersion(ctx, id)
		if err != nil {
			return fmt.Errorf("reconcile channel events: %w", err)
		}
		if !ok {
			return fmt.Errorf("reconcile channel events: review %s has channel.changed events but no versions", id)
		}
		if _, err := s.Append(ctx, &ccevent.Event{
			SubjectID: id, Origin: ccevent.OriginSystem, Type: store.EventChannelChanged,
			Payload: wire.Event(store.EventChannelChanged, v.VersionNumber, map[string]any{"connected": false}),
		}); err != nil {
			return err
		}
	}
	return nil
}

// sweepLoop periodically fails human AI-bar requests left pending past the TTL,
// so a request no live session ever dispatched stops showing as "queued"
// forever. It exits when the context is cancelled.
func (rv *review) sweepLoop(ctx context.Context, s *ccd.Server) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := rv.sweepStalePending(ctx, store.New(s.DB()), s.Append, time.Now().Add(-stalePendingTTL)); err != nil {
				rv.log.Printf("sweep stale pending: %v", err)
			}
		}
	}
}

// channelState classifies this window's channel route. active requires a proven
// round trip (the model acked a delivered channel tag) AND a channel consumer
// currently attached to this review's SSE stream — presence alone can never
// produce active, because Claude Code silently drops channel notifications when
// channels are unavailable. An attached or recently-polling but unproven consumer
// is pending; no consumer is inactive. The pid key keeps window A's signals from
// lighting up window B's start.
func channelState(act *ccd.Activity, reviewID, scope string, pid int) string {
	attached := act.Attached(reviewID, channelConsumer, pid)
	if attached && act.Proven(pid) {
		return "active"
	}
	if attached || act.PolledSince(scope, channelConsumer, pid, channelPollWindow) {
		return "pending"
	}
	return "inactive"
}

func reviewURL(httpPort int, slug string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/s/%s", httpPort, slug)
}

// emit appends a tagged-union event through the daemon's single Append
// chokepoint, stamping the version number inside the payload.
func emit(ctx context.Context, ap ccd.AppendFunc, subjectID, origin, typ string, version int, fields map[string]any) {
	_, _ = ap(ctx, &ccevent.Event{
		SubjectID: subjectID, Origin: origin, Type: typ, Payload: wire.Event(typ, version, fields),
	})
}
