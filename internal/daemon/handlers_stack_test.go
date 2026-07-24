package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/store"

	_ "modernc.org/sqlite"
)

// graphiteDDL mirrors gt 1.8.6's branch_metadata table (full column set; the
// capture layer reads only branch_name + parent_branch_name).
const graphiteDDL = `CREATE TABLE IF NOT EXISTS branch_metadata (
	branch_name TEXT PRIMARY KEY,
	parent_branch_name TEXT,
	parent_branch_revision TEXT,
	last_submitted_version TEXT,
	state TEXT,
	children TEXT,
	branch_revision TEXT,
	validation_result TEXT,
	parent_head_revision TEXT
)`

func gitCommonDir(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitRun(t, repo, "rev-parse", "--path-format=absolute", "--git-common-dir"))
}

func writeGraphiteConfig(t *testing.T, repo, trunk string) {
	t.Helper()
	writeFile(t, gitCommonDir(t, repo), ".graphite_repo_config",
		fmt.Sprintf(`{"trunk":%q,"trunks":["decoy-should-be-ignored"]}`, trunk))
}

func openMetaDB(t *testing.T, repo string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(gitCommonDir(t, repo), ".graphite_metadata.db"))
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	if _, err := db.Exec(graphiteDDL); err != nil {
		_ = db.Close()
		t.Fatalf("create branch_metadata: %v", err)
	}
	return db
}

func setTrunkMeta(t *testing.T, repo, trunk string) {
	t.Helper()
	db := openMetaDB(t, repo)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(
		`INSERT OR REPLACE INTO branch_metadata (branch_name, parent_branch_name, validation_result) VALUES (?, NULL, 'TRUNK')`,
		trunk); err != nil {
		t.Fatalf("insert trunk metadata: %v", err)
	}
}

// setBranchMeta records a stack row with parent_branch_revision set to the
// parent's real tip at track time — the column the capture layer ignores.
func setBranchMeta(t *testing.T, repo, branch, parent string) {
	t.Helper()
	rev := strings.TrimSpace(gitRun(t, repo, "rev-parse", "refs/heads/"+parent))
	db := openMetaDB(t, repo)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(
		`INSERT OR REPLACE INTO branch_metadata (branch_name, parent_branch_name, parent_branch_revision, children, validation_result)
		 VALUES (?, ?, ?, '[]', 'VALID')`,
		branch, parent, rev); err != nil {
		t.Fatalf("insert branch metadata: %v", err)
	}
}

func branchCommit(t *testing.T, repo, branch, parent, file, content string) {
	t.Helper()
	gitRun(t, repo, "checkout", "-qb", branch, parent)
	writeFile(t, repo, file, content)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", branch)
	setBranchMeta(t, repo, branch, parent)
}

// stackRepo turns testServer's plain-git repo into a Graphite stack: trunk main,
// committed branches feat-a (on main) and feat-b (on feat-a), feat-b checked out.
func stackRepo(t *testing.T, repo string) {
	t.Helper()
	writeGraphiteConfig(t, repo, "main")
	setTrunkMeta(t, repo, "main")
	branchCommit(t, repo, "feat-a", "main", "a.go", "package a\n\nfunc A() {}\n")
	branchCommit(t, repo, "feat-b", "feat-a", "b.go", "package b\n\nfunc B() {}\n")
}

func stackStart(ctx context.Context, t *testing.T, s *Server, repo string) Response {
	t.Helper()
	stackRepo(t, repo)
	resp := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !resp.OK {
		t.Fatalf("stack start: %s", resp.Error)
	}
	return resp
}

func withAnnotations(req Request, items ...AnnotateInput) Request {
	req.Annotations = items
	return req
}

func submitStackOrg(ctx context.Context, t *testing.T, s *Server, req Request, version int, sectionKey string, paths ...string) {
	t.Helper()
	files := make([]store.ChapterFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, store.ChapterFile{Path: p, Risk: "low", Rationale: "r", Lines: []store.LineNote{}})
	}
	r := req
	r.Organization = &store.Organization{Chapters: []store.Chapter{{Title: "All", Summary: "s", Files: files}}}
	r.VersionNumber = version
	r.SectionKey = sectionKey
	if resp := s.handleSubmitOrganization(ctx, r); !resp.OK {
		t.Fatalf("submit organization for %q: %s", sectionKey, resp.Error)
	}
}

func countVersions(ctx context.Context, t *testing.T, s *Server, reviewID string) int {
	t.Helper()
	vs, err := s.store.ListVersions(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	return len(vs)
}

func sectionsByBranch(sections []store.Section) map[string]store.Section {
	out := make(map[string]store.Section, len(sections))
	for _, sec := range sections {
		out[sec.Key()] = sec
	}
	return out
}

func TestStackStartCreatesOrderedSections(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	started := stackStart(ctx, t, s, repo)

	if started.Stack == nil || started.Stack.Trunk != "main" {
		t.Fatalf("started.Stack = %+v, want trunk main", started.Stack)
	}
	if !reflect.DeepEqual(started.Stack.Branches, []string{"feat-a", "feat-b"}) {
		t.Fatalf("stack branches = %v, want [feat-a feat-b]", started.Stack.Branches)
	}
	meta, ok, err := s.store.GetReviewMeta(ctx, started.ReviewID)
	if err != nil || !ok || !meta.Stack {
		t.Fatalf("review meta = %+v ok=%v err=%v, want stack=true", meta, ok, err)
	}

	sections := s.latestSections(ctx, t, started.ReviewID)
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2 (clean tree = no pending section)", len(sections))
	}
	a, b := sections[0], sections[1]
	if a.Position != 0 || a.Branch != "feat-a" || a.ParentBranch != "main" || a.Pending || a.Key() != "feat-a" {
		t.Fatalf("section 0 = %+v, want feat-a on main, non-pending", a)
	}
	if b.Position != 1 || b.Branch != "feat-b" || b.ParentBranch != "feat-a" || b.Pending || b.Key() != "feat-b" {
		t.Fatalf("section 1 = %+v, want feat-b on feat-a, non-pending", b)
	}
	// Each section carries only its own branch's diff.
	aFiles, _ := a.Files()
	bFiles, _ := b.Files()
	if len(aFiles) != 1 || aFiles[0].Path != "a.go" {
		t.Fatalf("feat-a files = %+v, want [a.go]", aFiles)
	}
	if len(bFiles) != 1 || bFiles[0].Path != "b.go" {
		t.Fatalf("feat-b files = %+v, want [b.go]", bFiles)
	}

	rf := parseReviewFiles(t, s.handleReviewFiles(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}).ReviewFiles)
	if !rf.Stack || len(rf.Sections) != 2 {
		t.Fatalf("review-files = %+v, want stack with 2 sections", rf)
	}
	if rf.Sections[0].SectionKey != "feat-a" || rf.Sections[1].SectionKey != "feat-b" {
		t.Fatalf("section keys = %q,%q, want feat-a,feat-b", rf.Sections[0].SectionKey, rf.Sections[1].SectionKey)
	}
}

func TestFlatStartCreatesOnePendingSection(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar X int\n")
	started := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	if started.Stack != nil {
		t.Fatalf("flat start returned stack info %+v", started.Stack)
	}
	if meta, _, _ := s.store.GetReviewMeta(ctx, started.ReviewID); meta.Stack {
		t.Fatal("flat review meta marked stack")
	}
	sections := s.latestSections(ctx, t, started.ReviewID)
	if len(sections) != 1 || !sections[0].Pending || sections[0].Key() != "" {
		t.Fatalf("sections = %+v, want one pending section keyed ''", sections)
	}
}

func TestStackBaseForcesFlat(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	stackRepo(t, repo)
	started := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Base: "main"})
	if !started.OK {
		t.Fatalf("start --base: %s", started.Error)
	}
	if started.Stack != nil {
		t.Fatalf("--base returned stack info %+v, want flat", started.Stack)
	}
	if meta, _, _ := s.store.GetReviewMeta(ctx, started.ReviewID); meta.Stack {
		t.Fatal("--base review marked stack")
	}
	sections := s.latestSections(ctx, t, started.ReviewID)
	if len(sections) != 1 || !sections[0].Pending {
		t.Fatalf("sections = %+v, want one flat pending section", sections)
	}
	// The flat diff spans the whole stack (a.go + b.go against main).
	if files, _ := sections[0].Files(); len(files) != 2 {
		t.Fatalf("flat --base files = %+v, want a.go + b.go", files)
	}
}

func TestStackResumeRejectsBase(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	stackStart(ctx, t, s, repo)
	resp := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Base: "main"})
	if resp.OK || !strings.Contains(resp.Error, "pinned") {
		t.Fatalf("resume with --base: ok=%v err=%q, want pinned rejection", resp.OK, resp.Error)
	}
}

func TestStackResumeDedupsAcrossRestack(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	started := stackStart(ctx, t, s, repo)

	// An unchanged resume reuses v1.
	again := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !again.OK || again.Version != started.Version {
		t.Fatalf("unchanged resume: version=%d, want %d (dedup)", again.Version, started.Version)
	}

	// A content-identical restack rewrites every sha but not one section's diff
	// bytes; the per-position dedup must still reuse v1.
	gitRun(t, repo, "checkout", "-q", "feat-a")
	gitRun(t, repo, "commit", "-q", "--amend", "-m", "feat-a reworded")
	gitRun(t, repo, "checkout", "-q", "feat-b")
	gitRun(t, repo, "rebase", "-q", "feat-a")

	restacked := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !restacked.OK || restacked.Version != started.Version {
		t.Fatalf("restack resume: version=%d err=%q, want %d (restack-stable dedup)", restacked.Version, restacked.Error, started.Version)
	}
	if n := countVersions(ctx, t, s, started.ReviewID); n != 1 {
		t.Fatalf("versions = %d, want 1 (no new version across restack)", n)
	}
}

func TestStackAmendCarriesUnchangedSectionOrg(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	started := stackStart(ctx, t, s, repo)
	req := Request{Session: "s1", ClaudePID: 100, Cwd: repo}

	submitStackOrg(ctx, t, s, req, started.Version, "feat-a", "a.go")
	submitStackOrg(ctx, t, s, req, started.Version, "feat-b", "b.go")

	// Amend feat-b: its section diff changes; feat-a is untouched.
	writeFile(t, repo, "b.go", "package b\n\nfunc B() { _ = 1 }\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "--amend", "-m", "feat-b v2")

	second := s.handleStart(ctx, req)
	if !second.OK || second.Version != started.Version+1 {
		t.Fatalf("amend start: version=%d err=%q, want %d", second.Version, second.Error, started.Version+1)
	}
	// feat-b changed, so an organize request is queued for the new version.
	if len(second.AIRequests) == 0 {
		t.Fatal("changed feat-b section queued no organize request")
	}
	sections := s.latestSections(ctx, t, started.ReviewID)
	orgs, err := s.store.GetOrganizationsByVersion(ctx, sections[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	byKey := sectionsByBranch(sections)
	if _, ok := orgs[byKey["feat-a"].ID]; !ok {
		t.Fatal("feat-a organization was not carried to the new version")
	}
	if _, ok := orgs[byKey["feat-b"].ID]; ok {
		t.Fatal("feat-b organization carried despite a changed diff")
	}
}

func TestStackCommentCopiesBranchAndPending(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	started := stackStart(ctx, t, s, repo)
	req := Request{Session: "s1", ClaudePID: 100, Cwd: repo}

	if resp := s.handleAnnotate(ctx, withAnnotations(req, AnnotateInput{
		Kind: "comment", SectionKey: "feat-a", FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "check A",
	})); !resp.OK {
		t.Fatalf("annotate feat-a: %s", resp.Error)
	}

	v, _, _ := s.store.LatestVersion(ctx, started.ReviewID)
	comments, err := s.store.ListCommentsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Branch != "feat-a" || comments[0].Pending {
		t.Fatalf("comment = %+v, want branch feat-a pending=false", comments[0])
	}
}

func TestStackFeedbackTagsThreads(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	started := stackStart(ctx, t, s, repo)
	// Dirty the tree so a pending section (branch feat-b, key "") joins the stack.
	writeFile(t, repo, "wip.go", "package p\nvar W int\n")
	if second := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}); !second.OK {
		t.Fatalf("dirty restart: %s", second.Error)
	}
	req := Request{Session: "s1", ClaudePID: 100, Cwd: repo}

	if resp := s.handleAnnotate(ctx, withAnnotations(req, AnnotateInput{
		Kind: "comment", SectionKey: "feat-b", FilePath: "b.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "B",
	})); !resp.OK {
		t.Fatalf("annotate feat-b: %s", resp.Error)
	}
	if resp := s.handleAnnotate(ctx, withAnnotations(req, AnnotateInput{
		Kind: "comment", SectionKey: "", FilePath: "wip.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "W",
	})); !resp.OK {
		t.Fatalf("annotate pending: %s", resp.Error)
	}

	v, _, _ := s.store.LatestVersion(ctx, started.ReviewID)
	fb, err := feedback.Build(ctx, s.store, started.ReviewID, v, time.Now())
	if err != nil {
		t.Fatalf("build feedback: %v", err)
	}
	got := make(map[string]bool, len(fb.Threads))
	for _, th := range fb.Threads {
		got[fmt.Sprintf("%s:%v", th.Branch, th.Pending)] = true
	}
	// The committed feat-b thread carries its branch; the pending thread
	// normalizes to branch "" (pending=true), safe for a naive branch!="" consumer.
	if !got["feat-b:false"] {
		t.Fatalf("threads missing committed feat-b thread (feat-b:false): %+v", fb.Threads)
	}
	if !got[":true"] {
		t.Fatalf("threads missing pending thread (\"\":true): %+v", fb.Threads)
	}
}
