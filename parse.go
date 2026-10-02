package main

import (
	"path/filepath"
	"strings"

	"github.com/google/shlex"
)

// prCreate holds the parts of a `gh pr create` invocation that rules check.
type prCreate struct {
	repo      string // owner/repo from -R / --repo
	cdDir     string // argument of a preceding `cd <dir>`
	labels    []string
	milestone string
	draft     bool
	dryRun    bool
	help      bool
	web       bool
}

// parsePRCreate tokenizes cmd as shell words and returns nil unless it runs `gh pr create/new`.
func parsePRCreate(cmd string) *prCreate {
	toks, err := shlex.Split(cmd)
	if err != nil {
		return nil
	}

	pr := &prCreate{}
	start := -1
	head := true // toks[j] is in command name position
	for j := 0; j < len(toks) && start < 0; j++ {
		wasHead := head
		head = isShellOp(toks[j]) || (head && strings.Contains(toks[j], "="))
		if !wasHead {
			continue
		}
		if toks[j] == "cd" && j+1 < len(toks) && !isShellOp(toks[j+1]) {
			pr.cdDir = toks[j+1]
			continue
		}
		if filepath.Base(toks[j]) != "gh" {
			continue
		}
		for k := j + 1; k+1 < len(toks) && !isShellOp(toks[k]); k++ {
			if repo, ok := flagValue(toks, &k, "-R", "--repo"); ok {
				pr.repo = normalizeRepo(repo)
				continue
			}
			if toks[k] == "pr" && (toks[k+1] == "create" || toks[k+1] == "new") {
				start = k + 2
				break
			}
		}
	}
	if start < 0 {
		return nil
	}

	for j := start; j < len(toks); j++ {
		a := toks[j]
		if isShellOp(a) || a == "--" {
			break // stop at the next command
		}
		if v, ok := flagValue(toks, &j, "-l", "--label"); ok {
			pr.labels = append(pr.labels, splitCSV(v)...)
			continue
		}
		if v, ok := flagValue(toks, &j, "-m", "--milestone"); ok {
			pr.milestone = v
			continue
		}
		if v, ok := flagValue(toks, &j, "-R", "--repo"); ok {
			pr.repo = normalizeRepo(v)
			continue
		}
		switch a {
		case "-d", "--draft", "--draft=true":
			pr.draft = true
		case "--dry-run":
			pr.dryRun = true
		case "-h", "--help":
			pr.help = true
		case "-w", "--web":
			pr.web = true
		case "-a", "--assignee", "-B", "--base", "-b", "--body", "-F", "--body-file", "-H", "--head",
			"-p", "--project", "--recover", "-r", "--reviewer", "-T", "--template", "-t", "--title":
			j++ // flags taking a value; skip the value even if it looks like a flag
		}
	}
	return pr
}

// flagValue returns the value if toks[*i] is the short or long flag, advancing *i for the `flag value` form.
func flagValue(toks []string, i *int, short, long string) (string, bool) {
	a := toks[*i]
	if v, ok := strings.CutPrefix(a, long+"="); ok {
		return v, true
	}
	if a != short && a != long {
		return "", false
	}
	if *i+1 >= len(toks) {
		return "", true
	}
	*i++
	return toks[*i], true
}

// dir returns the directory gh runs in.
func (pr *prCreate) dir(cwd string) string {
	switch {
	case pr.cdDir == "":
		return cwd
	case filepath.IsAbs(pr.cdDir):
		return pr.cdDir
	default:
		return filepath.Join(cwd, pr.cdDir)
	}
}

func splitCSV(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isShellOp(s string) bool {
	switch s {
	case "|", "||", "&&", ";", "&", ">", ">>", "<", "<<":
		return true
	}
	return false
}
