package urlparse

import (
	"regexp"
	"strings"
)

// GitHub URL detection.
//
// Paper abstracts frequently include a "Code: https://github.com/owner/repo"
// line or just a bare `github.com/owner/repo` reference. The regex matches
// both forms; we normalize the result to a canonical `https://github.com/<owner>/<repo>`
// URL (stripping any `.git` suffix, branch/path, query string, or trailing
// punctuation), so the rendered link always lands on the repo root regardless
// of how the author formatted it. Multiple matches → first one wins (papers
// almost always list the primary repo first).

// Match `https?://github.com/<owner>/<repo>` with optional path/branch.
//
// Note: the `[A-Za-z0-9._-]*` for owner/repo is intentionally permissive —
// it allows dotted repo names like `my.repo` — but it also greedily eats a
// trailing `.git` or trailing punctuation (`.`, `,`, `;`, `)`, etc.). The
// Go code below strips those in `cleanSegment`.
var githubURLRe = regexp.MustCompile(`(?i)\bhttps?://github\.com/([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9][A-Za-z0-9._-]*)(?:\.git)?(?:[/#?][^\s)<>"']*)?`)

// Bare `github.com/<owner>/<repo>` (no scheme). The leading non-word
// boundary prevents matching `https://github.com/...` (which is already
// handled by githubURLRe) and rejects spurious matches like
// `mygithub.com/foo/bar`.
var githubBareRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9./])github\.com/([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9][A-Za-z0-9._-]*)(?:\.git)?(?:[/#?][^\s)<>"']*)?`)

// ExtractGitHubURL scans text for the first GitHub repository URL and
// returns a normalized https://github.com/<owner>/<repo> form. Returns ""
// if no repository URL is found.
func ExtractGitHubURL(text string) string {
	if text == "" {
		return ""
	}

	var owner, repo string
	if m := githubURLRe.FindStringSubmatch(text); len(m) >= 3 {
		owner, repo = m[1], m[2]
	} else if m := githubBareRe.FindStringSubmatch(text); len(m) >= 3 {
		owner, repo = m[1], m[2]
	} else {
		return ""
	}

	owner = cleanSegment(owner)
	repo = cleanSegment(repo)
	if owner == "" || repo == "" {
		return ""
	}
	return "https://github.com/" + owner + "/" + repo
}

// cleanSegment strips a trailing `.git` suffix and any trailing
// punctuation (`.`, `,`, `;`, `:`, `!`, `?`, `)`, `]`, `}`) that the
// regex's permissive character class greedily consumed. The regex
// guarantees the first char is `[A-Za-z0-9]` and there is at least one
// such char, so trimming suffix/trailing punct can't produce an empty
// string for a valid match.
func cleanSegment(s string) string {
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, ".,;:!?)]}")
	return s
}
