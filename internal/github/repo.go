package github

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

const host = "github.com"

// Repo names a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// PRRef names one pull request.
type PRRef struct {
	Repo   Repo
	Number int
}

func (r PRRef) String() string { return r.Repo.String() + "#" + strconv.Itoa(r.Number) }

// ParsePRRef reads a pull request URL, owner/name#N, #N, or N. The last two
// forms name a pull request in defaultRepo, which must then be non-nil.
func ParsePRRef(s string, defaultRepo *Repo) (PRRef, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		return parsePRURL(s)
	}
	repoPart, numPart, qualified := strings.Cut(s, "#")
	if !qualified {
		numPart, repoPart = s, ""
	}
	number, err := parseNumber(numPart)
	if err != nil {
		return PRRef{}, fmt.Errorf("parse pull request %q: %w", s, err)
	}
	if repoPart == "" {
		if defaultRepo == nil {
			return PRRef{}, fmt.Errorf("parse pull request %q: no repository to resolve it in", s)
		}
		return PRRef{Repo: *defaultRepo, Number: number}, nil
	}
	repo, err := parseRepoPath(repoPart)
	if err != nil {
		return PRRef{}, fmt.Errorf("parse pull request %q: %w", s, err)
	}
	return PRRef{Repo: repo, Number: number}, nil
}

func parsePRURL(s string) (PRRef, error) {
	u, err := url.Parse(s)
	if err != nil {
		return PRRef{}, fmt.Errorf("parse pull request URL %q: %w", s, err)
	}
	if !strings.EqualFold(u.Hostname(), host) {
		return PRRef{}, fmt.Errorf("parse pull request URL %q: host is not %s", s, host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return PRRef{}, fmt.Errorf("parse pull request URL %q: path is not /<owner>/<name>/pull/<number>", s)
	}
	number, err := parseNumber(parts[3])
	if err != nil {
		return PRRef{}, fmt.Errorf("parse pull request URL %q: %w", s, err)
	}
	return PRRef{Repo: Repo{Owner: parts[0], Name: parts[1]}, Number: number}, nil
}

func parseNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a pull request number", s)
	}
	return n, nil
}

func parseRepoPath(p string) (Repo, error) {
	owner, name, ok := strings.Cut(strings.TrimSuffix(strings.Trim(p, "/"), ".git"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("%q is not <owner>/<name>", p)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// RepoFromRemote reads the GitHub repository dir's origin remote points at.
func RepoFromRemote(ctx context.Context, dir string) (Repo, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return Repo{}, fmt.Errorf("read the origin url of %s: %w", dir, err)
	}
	repo, err := parseRemote(strings.TrimSpace(string(out)))
	if err != nil {
		return Repo{}, fmt.Errorf("origin of %s: %w", dir, err)
	}
	return repo, nil
}

func parseRemote(raw string) (Repo, error) {
	var hostname, p string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Repo{}, fmt.Errorf("parse remote %q: %w", raw, err)
		}
		hostname, p = u.Hostname(), u.Path
	} else {
		var ok bool
		hostname, p, ok = strings.Cut(raw, ":")
		if !ok {
			return Repo{}, fmt.Errorf("remote %q is neither a URL nor an scp-style address", raw)
		}
		if _, after, found := strings.Cut(hostname, "@"); found {
			hostname = after
		}
	}
	if !strings.EqualFold(hostname, host) {
		return Repo{}, fmt.Errorf("remote %q is not on %s", raw, host)
	}
	return parseRepoPath(p)
}
