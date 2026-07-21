package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
)

// activitySchema is the versioned wire schema of `export activity` documents,
// read by cc-transcript's disktruth.load_export.
const activitySchema = "cc-review.activity/1"

type activityExport struct {
	Schema       string               `json:"schema"`
	SessionID    string               `json:"session_id"`
	Turns        []activityTurn       `json:"turns"`
	Attributions []activityAttributed `json:"attributions"`
}

type activityTurn struct {
	TurnID      int64  `json:"turn_id"`
	RepoRoot    string `json:"repo_root"`
	StartedAtMs int64  `json:"started_at_ms"`
	EndedAtMs   int64  `json:"ended_at_ms"` // 0 while open
	TreeStart   string `json:"tree_start"`
	TreeEnd     string `json:"tree_end"` // '' while open
	Status      string `json:"status"`
}

type activityAttributed struct {
	ReviewID string          `json:"review_id"`
	Version  int             `json:"version"`
	FilePath string          `json:"file_path"`
	Ranges   []activityRange `json:"ranges"`
}

// activityRange serializes turn_id as an explicit null when unattributed —
// the Python reader requires the key on every range.
type activityRange struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	TurnID *int64 `json:"turn_id"`
}

// newExportCmd groups the cross-tool export verbs.
func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export cc-review's ledgers for other cc-family tools",
	}
	cmd.AddCommand(newExportActivityCmd())
	return cmd
}

// newExportActivityCmd emits the cc-review.activity/1 document for a session:
// its tree-bracketed turns plus version-dimensioned attribution ranges. It
// reads the store directly so the export works with no daemon running.
func newExportActivityCmd() *cobra.Command {
	var session string
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Print a session's turns and attributions as cc-review.activity/1 JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if session == "" {
				return errors.New("--session is required")
			}
			if err := paths.EnsureStateDir(); err != nil {
				return err
			}
			st, err := store.Open(cmd.Context(), paths.DBPath())
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			return writeActivity(cmd.Context(), cmd.OutOrStdout(), st, session)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id the export covers")
	return cmd
}

func writeActivity(ctx context.Context, w io.Writer, st *store.Store, session string) error {
	turns, err := vcs.NewTurnStore(st.DB()).ListTurnsBySession(ctx, session)
	if err != nil {
		return err
	}
	attributions, err := st.ListAttributionsBySession(ctx, session)
	if err != nil {
		return err
	}
	doc := activityExport{
		Schema:       activitySchema,
		SessionID:    session,
		Turns:        make([]activityTurn, 0, len(turns)),
		Attributions: make([]activityAttributed, 0, len(attributions)),
	}
	for _, t := range turns {
		doc.Turns = append(doc.Turns, activityTurn{
			TurnID: t.ID, RepoRoot: t.RepoRoot, StartedAtMs: t.StartedAt, EndedAtMs: t.EndedAt,
			TreeStart: t.TreeStart, TreeEnd: t.TreeEnd, Status: t.Status,
		})
	}
	for _, sa := range attributions {
		ranges := make([]activityRange, 0, len(sa.Ranges))
		for _, rg := range sa.Ranges {
			ranges = append(ranges, activityRange{Start: rg.Start, End: rg.End, TurnID: attributedTurn(rg.TurnID)})
		}
		doc.Attributions = append(doc.Attributions, activityAttributed{
			ReviewID: sa.ReviewID, Version: sa.Version, FilePath: sa.FilePath, Ranges: ranges,
		})
	}
	return json.NewEncoder(w).Encode(doc)
}

// attributedTurn maps the store's zero-means-unattributed turn id to the wire's
// explicit null.
func attributedTurn(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
