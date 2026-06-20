package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/yasyf/cc-interact/vcs"
)

// sliceSchema is the versioned wire schema of `cc-transcript slice` lines.
const sliceSchema = "cc-transcript.slice/1"

// provenanceItem is one tool call of a turn window, lifted from the slice
// contract; the field names are the slice CLI's, passed through to the SPA.
type provenanceItem struct {
	EventUUID string `json:"event_uuid"`
	TsMs      int64  `json:"ts_ms"`
	ToolName  string `json:"tool_name"`
	Summary   string `json:"summary"`
	FilePath  string `json:"file_path"`
}

type provenanceResponse struct {
	Provenance  []provenanceItem `json:"provenance"`
	Unavailable bool             `json:"provenance_unavailable"`
}

// handleTurnProvenance returns a turn's tool calls by shelling out to
// `cc-transcript slice` over the turn's time window. Results for closed turns
// are cached in memory only — the store is wipeable, the transcript is not
// ours to persist. A missing binary, a nonzero exit (1 = transcript missing),
// or schema skew all degrade to an empty list with provenance_unavailable set,
// logged once per session.
func (s *Server) handleTurnProvenance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad turn id", http.StatusBadRequest)
		return
	}
	turn, err := s.turns.GetTurn(r.Context(), id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if items, ok := s.cachedProvenance(id); ok {
		writeJSON(w, http.StatusOK, provenanceResponse{Provenance: items})
		return
	}
	items, err := sliceTurn(r.Context(), turn)
	if err != nil {
		s.warnNoSlice(turn.SessionID, err)
		writeJSON(w, http.StatusOK, provenanceResponse{Provenance: []provenanceItem{}, Unavailable: true})
		return
	}
	if turn.EndedAt != 0 {
		s.provMu.Lock()
		s.provCache[id] = items
		s.provMu.Unlock()
	}
	writeJSON(w, http.StatusOK, provenanceResponse{Provenance: items})
}

func (s *Server) cachedProvenance(id int64) ([]provenanceItem, bool) {
	s.provMu.Lock()
	defer s.provMu.Unlock()
	items, ok := s.provCache[id]
	return items, ok
}

func (s *Server) warnNoSlice(sessionID string, err error) {
	s.provMu.Lock()
	defer s.provMu.Unlock()
	if s.provWarned[sessionID] {
		return
	}
	s.provWarned[sessionID] = true
	s.log.Printf("provenance: cc-transcript slice unavailable for session %s: %v", sessionID, err)
}

// sliceTurn shells `cc-transcript slice` over the turn window — until now for
// a still-open turn — and parses its JSONL stdout.
func sliceTurn(ctx context.Context, turn vcs.Turn) ([]provenanceItem, error) {
	untilMs := turn.EndedAt
	if untilMs == 0 {
		untilMs = time.Now().UnixMilli()
	}
	out, err := exec.CommandContext(ctx, "cc-transcript", "slice", //nolint:gosec // G204: fixed cc-transcript subcommand; args are this tool's own session ID and timestamps, intentionally shelling out to the companion CLI.
		"--session", turn.SessionID, "--since", rfc3339Ms(turn.StartedAt), "--until", rfc3339Ms(untilMs)).Output()
	if err != nil {
		return nil, err
	}
	items := []provenanceItem{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			Schema string `json:"schema"`
			provenanceItem
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("parse slice line %q: %w", line, err)
		}
		if row.Schema != sliceSchema {
			return nil, fmt.Errorf("unexpected slice schema %q", row.Schema)
		}
		items = append(items, row.provenanceItem)
	}
	return items, nil
}

func rfc3339Ms(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}
