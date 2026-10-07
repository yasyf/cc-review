// Package githubtest serves a fake GitHub REST and GraphQL API for tests of
// code built on internal/github.
package githubtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/github"
)

var (
	operationName = regexp.MustCompile(`^\s*(?:query|mutation)\s+(\w+)`)
	snapshotAlias = regexp.MustCompile(`pr(\d+): pullRequest\(number: \d+\)`)
)

// StaticToken is a TokenSource that always yields itself.
type StaticToken string

// Token returns t.
func (t StaticToken) Token(context.Context) (string, error) { return string(t), nil }

// Write is one mutating request the server accepted: a REST POST, or a
// GraphQL mutation recorded with Path "graphql:<operation>" and its variables
// as Body.
type Write struct {
	Method string
	Path   string
	Login  string
	Body   map[string]any
}

type pullRequest struct {
	github.PullRequest
	repo          github.Repo
	cross         bool
	threads       []*github.Thread
	issueComments []github.RemoteComment
}

// Server is a fake GitHub. Tokens authenticate only after Login maps them to
// a user; PageSize bounds every GraphQL connection page it serves.
type Server struct {
	PageSize int

	srv         *httptest.Server
	mu          sync.Mutex
	logins      map[string]string
	defaults    map[github.Repo]string
	prs         map[github.Repo]map[int]*pullRequest
	mergeBases  map[string]string
	writes      []Write
	nextID      int64
	failStatus  int
	failMessage string
	clock       time.Time
}

// New starts a fake GitHub that closes when t ends.
func New(t testing.TB) *Server {
	s := &Server{
		PageSize:   100,
		logins:     map[string]string{},
		defaults:   map[github.Repo]string{},
		prs:        map[github.Repo]map[int]*pullRequest{},
		mergeBases: map[string]string{},
		nextID:     1000,
		clock:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /graphql", s.graphql)
	mux.HandleFunc("GET /repos/{owner}/{name}", s.repo)
	mux.HandleFunc("GET /repos/{owner}/{name}/compare/{spec...}", s.compare)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{number}/comments", s.createReviewComment)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{number}/comments/{id}/replies", s.reply)
	mux.HandleFunc("POST /repos/{owner}/{name}/issues/{number}/comments", s.createIssueComment)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{number}/reviews", s.submitReview)
	s.srv = httptest.NewServer(s.authenticate(mux))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the server's REST root; GraphQL is served at URL + "/graphql".
func (s *Server) URL() string { return s.srv.URL }

// Client returns a github.Client against the server authenticating with token.
func (s *Server) Client(token string) *github.Client {
	return github.New(StaticToken(token), github.WithBaseURL(s.srv.URL, s.srv.URL+"/graphql"))
}

// Login makes token authenticate as login. A login ending in [bot] is served
// as a Bot actor over GraphQL, the way GitHub serves an app's bot.
func (s *Server) Login(token, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins[token] = login
}

// SetDefaultBranch sets repo's default branch.
func (s *Server) SetDefaultBranch(repo github.Repo, branch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaults[repo] = branch
}

// SetMergeBase makes compare base...head in repo answer sha.
func (s *Server) SetMergeBase(repo github.Repo, base, head, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergeBases[repo.String()+" "+base+"..."+head] = sha
}

// AddPR seeds a pull request, filling NodeID, URL, State, and UpdatedAt when
// empty, and returns it as seeded.
func (s *Server) AddPR(repo github.Repo, pr github.PullRequest) github.PullRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pr.NodeID == "" {
		pr.NodeID = fmt.Sprintf("PR_%s_%d", repo, pr.Number)
	}
	if pr.URL == "" {
		pr.URL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr.Number)
	}
	if pr.State == "" {
		pr.State = "OPEN"
	}
	if pr.UpdatedAt.IsZero() {
		pr.UpdatedAt = s.clock
	}
	if s.prs[repo] == nil {
		s.prs[repo] = map[int]*pullRequest{}
	}
	s.prs[repo][pr.Number] = &pullRequest{PullRequest: pr, repo: repo}
	return pr
}

// MarkCrossRepository makes a seeded pull request's head live in a fork.
func (s *Server) MarkCrossRepository(repo github.Repo, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mustPR(repo, number).cross = true
}

// UpdatePR edits a seeded pull request in place.
func (s *Server) UpdatePR(repo github.Repo, number int, edit func(*github.PullRequest)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(&s.mustPR(repo, number).PullRequest)
}

// AddThread seeds a review thread, filling node and database IDs, URLs, and
// timestamps left empty, and returns it as seeded.
func (s *Server) AddThread(repo github.Repo, number int, thread github.Thread) github.Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.mustPR(repo, number)
	if thread.NodeID == "" {
		thread.NodeID = "PRRT_" + strconv.FormatInt(s.id(), 10)
	}
	for i := range thread.Comments {
		thread.Comments[i] = s.fill(pr, thread.Comments[i], "PRRC_", "#discussion_r")
	}
	pr.threads = append(pr.threads, &thread)
	return thread
}

// AddReply appends a comment to a seeded thread and returns it as seeded.
func (s *Server) AddReply(repo github.Repo, number int, threadNodeID string, comment github.RemoteComment) github.RemoteComment {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.mustPR(repo, number)
	thread := s.mustThread(pr, threadNodeID)
	comment = s.fill(pr, comment, "PRRC_", "#discussion_r")
	thread.Comments = append(thread.Comments, comment)
	return comment
}

// AddIssueComment seeds a conversation comment and returns it as seeded.
func (s *Server) AddIssueComment(repo github.Repo, number int, comment github.RemoteComment) github.RemoteComment {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.mustPR(repo, number)
	comment = s.fill(pr, comment, "IC_", "#issuecomment-")
	pr.issueComments = append(pr.issueComments, comment)
	return comment
}

// SetResolved resolves or reopens a seeded thread, as a coworker would.
func (s *Server) SetResolved(repo github.Repo, number int, threadNodeID string, resolved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mustThread(s.mustPR(repo, number), threadNodeID).IsResolved = resolved
}

// Snapshot returns the server's current state of one pull request.
func (s *Server) Snapshot(repo github.Repo, number int) github.PRSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.mustPR(repo, number)
	snap := github.PRSnapshot{PR: pr.PullRequest, IssueComments: slices.Clone(pr.issueComments)}
	for _, t := range pr.threads {
		thread := *t
		thread.Comments = slices.Clone(t.Comments)
		snap.Threads = append(snap.Threads, thread)
	}
	return snap
}

// FailWrites makes every later write answer status with message; status 0
// lets writes through again.
func (s *Server) FailWrites(status int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus, s.failMessage = status, message
}

// Writes returns every write the server accepted, oldest first.
func (s *Server) Writes() []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes)
}

func (s *Server) id() int64 {
	s.nextID++
	return s.nextID
}

func (s *Server) fill(pr *pullRequest, c github.RemoteComment, prefix, anchor string) github.RemoteComment {
	if c.DatabaseID == 0 {
		c.DatabaseID = s.id()
	}
	if c.NodeID == "" {
		c.NodeID = prefix + strconv.FormatInt(c.DatabaseID, 10)
	}
	if c.URL == "" {
		c.URL = pr.URL + anchor + strconv.FormatInt(c.DatabaseID, 10)
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = s.clock
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	return c
}

func (s *Server) mustPR(repo github.Repo, number int) *pullRequest {
	pr, ok := s.prs[repo][number]
	if !ok {
		panic(fmt.Sprintf("githubtest: no pull request %s#%d seeded", repo, number))
	}
	return pr
}

func (s *Server) mustThread(pr *pullRequest, nodeID string) *github.Thread {
	for _, t := range pr.threads {
		if t.NodeID == nodeID {
			return t
		}
	}
	panic(fmt.Sprintf("githubtest: no thread %s on %s#%d", nodeID, pr.repo, pr.Number))
}

type loginKey struct{}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		login, known := s.logins[token]
		s.mu.Unlock()
		if !ok || !known {
			writeError(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loginKey{}, login)))
	})
}

func loginOf(r *http.Request) string { return r.Context().Value(loginKey{}).(string) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

func (s *Server) record(w http.ResponseWriter, r *http.Request, path string, body map[string]any) bool {
	if s.failStatus != 0 {
		writeError(w, s.failStatus, s.failMessage)
		return false
	}
	s.writes = append(s.writes, Write{Method: r.Method, Path: path, Login: loginOf(r), Body: body})
	return true
}

func repoOf(r *http.Request) github.Repo {
	return github.Repo{Owner: r.PathValue("owner"), Name: r.PathValue("name")}
}

func (s *Server) prOf(w http.ResponseWriter, r *http.Request) (*pullRequest, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	pr, ok := s.prs[repoOf(r)][number]
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return nil, false
	}
	return pr, true
}

func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON")
		return nil, false
	}
	return body, true
}

func (s *Server) repo(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	branch, ok := s.defaults[repoOf(r)]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"full_name": repoOf(r).String(), "default_branch": branch})
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sha, ok := s.mergeBases[repoOf(r).String()+" "+r.PathValue("spec")]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"merge_base_commit": map[string]string{"sha": sha}})
}

func (s *Server) restComment(c github.RemoteComment) map[string]any {
	return map[string]any{
		"id":         c.DatabaseID,
		"node_id":    c.NodeID,
		"body":       c.Body,
		"html_url":   c.URL,
		"created_at": c.CreatedAt,
		"updated_at": c.UpdatedAt,
		"user":       map[string]string{"login": c.AuthorLogin, "avatar_url": c.AuthorAvatarURL},
	}
}

func (s *Server) authored(r *http.Request, body string) github.RemoteComment {
	login := loginOf(r)
	return github.RemoteComment{AuthorLogin: login, AuthorAvatarURL: "https://avatars.example/" + login, Body: body}
}

func intField(body map[string]any, key string) int {
	n, _ := body[key].(float64)
	return int(n)
}

func stringField(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return s
}

func (s *Server) createReviewComment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.prOf(w, r)
	if !ok {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	if commit := stringField(body, "commit_id"); commit != pr.HeadRefOid {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("commit_id %q is not the head of #%d", commit, pr.Number))
		return
	}
	if !s.record(w, r, r.URL.Path, body) {
		return
	}
	thread := &github.Thread{NodeID: "PRRT_" + strconv.FormatInt(s.id(), 10), Path: stringField(body, "path"), SubjectType: "LINE"}
	if stringField(body, "subject_type") == "file" {
		thread.SubjectType = "FILE"
	} else {
		thread.Line, thread.OriginalLine, thread.DiffSide = intField(body, "line"), intField(body, "line"), stringField(body, "side")
		thread.StartLine, thread.OriginalStartLine, thread.StartDiffSide = intField(body, "start_line"), intField(body, "start_line"), stringField(body, "start_side")
	}
	comment := s.fill(pr, s.authored(r, stringField(body, "body")), "PRRC_", "#discussion_r")
	thread.Comments = []github.RemoteComment{comment}
	pr.threads = append(pr.threads, thread)
	writeJSON(w, http.StatusCreated, s.restComment(comment))
}

func (s *Server) reply(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.prOf(w, r)
	if !ok {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	idx := slices.IndexFunc(pr.threads, func(t *github.Thread) bool {
		return slices.ContainsFunc(t.Comments, func(c github.RemoteComment) bool { return c.DatabaseID == id })
	})
	if idx < 0 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	if !s.record(w, r, r.URL.Path, body) {
		return
	}
	comment := s.fill(pr, s.authored(r, stringField(body, "body")), "PRRC_", "#discussion_r")
	pr.threads[idx].Comments = append(pr.threads[idx].Comments, comment)
	writeJSON(w, http.StatusCreated, s.restComment(comment))
}

func (s *Server) createIssueComment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.prOf(w, r)
	if !ok {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok || !s.record(w, r, r.URL.Path, body) {
		return
	}
	comment := s.fill(pr, s.authored(r, stringField(body, "body")), "IC_", "#issuecomment-")
	pr.issueComments = append(pr.issueComments, comment)
	writeJSON(w, http.StatusCreated, s.restComment(comment))
}

func (s *Server) submitReview(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.prOf(w, r)
	if !ok {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	event := stringField(body, "event")
	state := map[string]string{"COMMENT": "COMMENTED", "APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED"}[event]
	switch {
	case state == "":
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("event %q is not COMMENT, APPROVE, or REQUEST_CHANGES", event))
		return
	case event != "COMMENT" && loginOf(r) == pr.AuthorLogin:
		writeError(w, http.StatusUnprocessableEntity, "Review Can not approve or request changes on your own pull request")
		return
	}
	if !s.record(w, r, r.URL.Path, body) {
		return
	}
	reviewer := github.Reviewer{Login: loginOf(r), AvatarURL: "https://avatars.example/" + loginOf(r), State: state}
	if i := slices.IndexFunc(pr.Reviewers, func(rv github.Reviewer) bool { return rv.Login == reviewer.Login }); i >= 0 {
		pr.Reviewers[i] = reviewer
	} else {
		pr.Reviewers = append(pr.Reviewers, reviewer)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": s.id(), "state": state})
}

type notFound string

func (n notFound) Error() string { return string(n) }

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func (s *Server) graphql(w http.ResponseWriter, r *http.Request) {
	var req graphQLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON")
		return
	}
	match := operationName.FindStringSubmatch(req.Query)
	if match == nil {
		writeError(w, http.StatusBadRequest, "githubtest: anonymous operation")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.operation(w, r, match[1], req)
	var missing notFound
	switch {
	case errors.As(err, &missing):
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"type": "NOT_FOUND", "message": err.Error()}}})
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	case data != nil:
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	}
}

func (s *Server) operation(w http.ResponseWriter, r *http.Request, name string, req graphQLRequest) (map[string]any, error) {
	vars := req.Variables
	repo := github.Repo{Owner: stringField(vars, "owner"), Name: stringField(vars, "name")}
	switch name {
	case "Viewer":
		return map[string]any{"viewer": map[string]string{"login": loginOf(r)}}, nil
	case "PullRequest":
		pr, ok := s.prs[repo][intField(vars, "number")]
		if !ok {
			return nil, notFound(fmt.Sprintf("Could not resolve to a PullRequest with the number of %d.", intField(vars, "number")))
		}
		return repository(map[string]any{"pullRequest": s.prJSON(pr, false)}), nil
	case "OpenPRs":
		return repository(map[string]any{"pullRequests": s.openPRs(repo, vars)}), nil
	case "Snapshot":
		return s.snapshot(repo, req.Query)
	case "ThreadOfComment":
		pr, ok := s.prs[repo][intField(vars, "number")]
		if !ok {
			return nil, notFound(fmt.Sprintf("Could not resolve to a PullRequest with the number of %d.", intField(vars, "number")))
		}
		nodes := make([]any, 0, len(pr.threads))
		for _, t := range pr.threads {
			first := map[string]any{"id": t.Comments[0].NodeID}
			nodes = append(nodes, map[string]any{"id": t.NodeID, "comments": map[string]any{"nodes": []any{first}}})
		}
		page := map[string]any{"pageInfo": map[string]any{"hasPreviousPage": false, "startCursor": nil}, "nodes": nodes}
		return repository(map[string]any{"pullRequest": map[string]any{"reviewThreads": page}}), nil
	case "ReviewThreadsPage", "IssueCommentsPage", "ThreadCommentsPage", "CheckContextsPage":
		return s.page(name, stringField(vars, "id"), stringField(vars, "after"))
	case "ResolveThread", "UnresolveThread":
		return s.resolve(w, r, name, stringField(vars, "id"), vars)
	}
	return nil, fmt.Errorf("githubtest: unknown operation %s", name)
}

func repository(fields map[string]any) map[string]any { return map[string]any{"repository": fields} }

func (s *Server) snapshot(repo github.Repo, query string) (map[string]any, error) {
	fields := map[string]any{}
	for _, m := range snapshotAlias.FindAllStringSubmatch(query, -1) {
		number, _ := strconv.Atoi(m[1])
		pr, ok := s.prs[repo][number]
		if !ok {
			return nil, notFound(fmt.Sprintf("Could not resolve to a PullRequest with the number of %d.", number))
		}
		fields["pr"+m[1]] = s.prJSON(pr, true)
	}
	return repository(fields), nil
}

func (s *Server) openPRs(repo github.Repo, vars map[string]any) map[string]any {
	head, base := stringField(vars, "head"), stringField(vars, "base")
	var nodes []any
	for _, number := range slices.Sorted(maps.Keys(s.prs[repo])) {
		pr := s.prs[repo][number]
		if pr.State != "OPEN" || head != "" && pr.HeadRefName != head || base != "" && pr.BaseRefName != base {
			continue
		}
		nodes = append(nodes, s.prJSON(pr, false))
	}
	return s.connection(nodes, stringField(vars, "after"))
}

func (s *Server) connection(nodes []any, after string) map[string]any {
	start := 0
	if after != "" {
		start, _ = strconv.Atoi(after)
	}
	end := min(start+s.PageSize, len(nodes))
	page := nodes[start:end]
	if page == nil {
		page = []any{}
	}
	return map[string]any{
		"pageInfo": map[string]any{"hasNextPage": end < len(nodes), "endCursor": strconv.Itoa(end)},
		"nodes":    page,
	}
}

func (s *Server) page(operation, id, after string) (map[string]any, error) {
	for _, prs := range s.prs {
		for _, pr := range prs {
			field, nodes, ok := s.connectionOf(pr, operation, id)
			if ok {
				return map[string]any{"node": map[string]any{field: s.connection(nodes, after)}}, nil
			}
		}
	}
	return nil, notFound(fmt.Sprintf("Could not resolve to a node with the global id of '%s'", id))
}

func (s *Server) connectionOf(pr *pullRequest, operation, id string) (string, []any, bool) {
	switch {
	case operation == "ReviewThreadsPage" && pr.NodeID == id:
		return "reviewThreads", s.threadsJSON(pr), true
	case operation == "IssueCommentsPage" && pr.NodeID == id:
		return "comments", commentsJSON(pr.issueComments), true
	case operation == "CheckContextsPage" && rollupID(pr) == id:
		return "contexts", checksJSON(pr.Checks), true
	case operation == "ThreadCommentsPage":
		for _, t := range pr.threads {
			if t.NodeID == id {
				return "comments", commentsJSON(t.Comments), true
			}
		}
	}
	return "", nil, false
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request, operation, id string, vars map[string]any) (map[string]any, error) {
	for _, prs := range s.prs {
		for _, pr := range prs {
			for _, t := range pr.threads {
				if t.NodeID != id {
					continue
				}
				if !s.record(w, r, "graphql:"+operation, vars) {
					return nil, nil
				}
				t.IsResolved = operation == "ResolveThread"
				field := map[bool]string{true: "resolveReviewThread", false: "unresolveReviewThread"}[t.IsResolved]
				return map[string]any{field: map[string]any{"thread": map[string]any{"id": id}}}, nil
			}
		}
	}
	return nil, notFound(fmt.Sprintf("Could not resolve to a node with the global id of '%s'", id))
}

func rollupID(pr *pullRequest) string { return "SCR_" + pr.NodeID }

func actorJSON(login, avatar string) map[string]any {
	if bot, ok := strings.CutSuffix(login, "[bot]"); ok {
		return map[string]any{"__typename": "Bot", "login": bot, "avatarUrl": avatar}
	}
	return map[string]any{"__typename": "User", "login": login, "avatarUrl": avatar}
}

func (s *Server) prJSON(pr *pullRequest, sync bool) map[string]any {
	m := map[string]any{
		"id":                pr.NodeID,
		"number":            pr.Number,
		"title":             pr.Title,
		"body":              pr.Body,
		"state":             pr.State,
		"isDraft":           pr.Draft,
		"url":               pr.URL,
		"mergeable":         pr.Mergeable,
		"updatedAt":         pr.UpdatedAt,
		"headRefName":       pr.HeadRefName,
		"headRefOid":        pr.HeadRefOid,
		"baseRefName":       pr.BaseRefName,
		"isCrossRepository": pr.cross,
		"author":            actorJSON(pr.AuthorLogin, "https://avatars.example/"+pr.AuthorLogin),
		"statusCheckRollup": nil,
	}
	if len(pr.Checks) > 0 {
		m["statusCheckRollup"] = map[string]any{"id": rollupID(pr), "contexts": s.connection(checksJSON(pr.Checks), "")}
	}
	reviews, requests := []any{}, []any{}
	for _, rv := range pr.Reviewers {
		actor := actorJSON(rv.Login, rv.AvatarURL)
		if rv.State == "PENDING" {
			requests = append(requests, map[string]any{"requestedReviewer": actor})
			continue
		}
		reviews = append(reviews, map[string]any{"state": rv.State, "author": actor})
	}
	m["latestReviews"] = map[string]any{"nodes": reviews}
	m["reviewRequests"] = map[string]any{"nodes": requests}
	if sync {
		m["reviewThreads"] = s.connection(s.threadsJSON(pr), "")
		m["comments"] = s.connection(commentsJSON(pr.issueComments), "")
	}
	return m
}

func checksJSON(checks []github.Check) []any {
	out := make([]any, 0, len(checks))
	for _, c := range checks {
		if c.State == "PENDING" {
			out = append(out, map[string]any{"__typename": "CheckRun", "name": c.Name, "status": "IN_PROGRESS", "conclusion": nil, "detailsUrl": c.URL})
			continue
		}
		out = append(out, map[string]any{"__typename": "CheckRun", "name": c.Name, "status": "COMPLETED", "conclusion": c.State, "detailsUrl": c.URL})
	}
	return out
}

func nullable(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func (s *Server) threadsJSON(pr *pullRequest) []any {
	out := make([]any, 0, len(pr.threads))
	for _, t := range pr.threads {
		out = append(out, map[string]any{
			"id":                t.NodeID,
			"isResolved":        t.IsResolved,
			"isOutdated":        t.IsOutdated,
			"path":              t.Path,
			"subjectType":       t.SubjectType,
			"line":              nullable(t.Line),
			"startLine":         nullable(t.StartLine),
			"originalLine":      nullable(t.OriginalLine),
			"originalStartLine": nullable(t.OriginalStartLine),
			"diffSide":          t.DiffSide,
			"startDiffSide":     t.StartDiffSide,
			"comments":          s.connection(commentsJSON(t.Comments), ""),
		})
	}
	return out
}

func commentsJSON(comments []github.RemoteComment) []any {
	out := make([]any, 0, len(comments))
	for _, c := range comments {
		out = append(out, map[string]any{
			"id":             c.NodeID,
			"fullDatabaseId": strconv.FormatInt(c.DatabaseID, 10),
			"body":           c.Body,
			"url":            c.URL,
			"createdAt":      c.CreatedAt,
			"updatedAt":      c.UpdatedAt,
			"author":         actorJSON(c.AuthorLogin, c.AuthorAvatarURL),
		})
	}
	return out
}
