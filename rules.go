package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type config struct {
	Rules []rule `yaml:"rules"`
}

// rule is a set of requirements for PRs to repos matching Repos. An empty Repos matches every repository.
type rule struct {
	Repos            []string    `yaml:"repos"`
	RequireDraft     bool        `yaml:"require-draft"`
	ForbidWeb        bool        `yaml:"forbid-web"`
	RequireMilestone bool        `yaml:"require-milestone"`
	RequireLabels    []labelRule `yaml:"require-labels"`
}

// labelRule requires at least one label from OneOf. Hint is appended to the deny reason.
type labelRule struct {
	OneOf []string `yaml:"one-of"`
	Hint  string   `yaml:"hint"`
}

func configPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "cc-prguard", "config.yaml"), nil
}

// loadConfig returns an empty config when the file does not exist.
func loadConfig(p string) (*config, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return &config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	for i, r := range cfg.Rules {
		for j, l := range r.RequireLabels {
			if len(l.OneOf) == 0 {
				return nil, fmt.Errorf("parse %s: rules[%d].require-labels[%d]: one-of is empty", p, i, j)
			}
		}
	}
	return &cfg, nil
}

// matches reports false for an empty (unresolved) repo unless the rule has no Repos.
func (r *rule) matches(repo string) bool {
	if len(r.Repos) == 0 {
		return true
	}
	return slices.ContainsFunc(r.Repos, func(p string) bool {
		ok, _ := path.Match(p, repo)
		return ok
	})
}

// check returns the deny reason, or an empty string when pr satisfies every rule.
func (c *config) check(repo string, pr *prCreate) string {
	var fixes []string
	add := func(s string) {
		if !slices.Contains(fixes, s) {
			fixes = append(fixes, s)
		}
	}
	for _, r := range c.Rules {
		if !r.matches(repo) {
			continue
		}
		if r.ForbidWeb && pr.web {
			add("remove --web (rules cannot be checked in the browser flow)")
		}
		if r.RequireDraft && !pr.draft {
			add("add --draft")
		}
		if r.RequireMilestone && pr.milestone == "" {
			add("add --milestone <name>")
		}
		for _, l := range r.RequireLabels {
			if !slices.ContainsFunc(l.OneOf, func(s string) bool { return slices.Contains(pr.labels, s) }) {
				fix := "add --label with one of: " + strings.Join(l.OneOf, ", ")
				if l.Hint != "" {
					fix += " (" + l.Hint + ")"
				}
				add(fix)
			}
		}
	}
	if len(fixes) == 0 {
		return ""
	}

	target := repo
	if target == "" {
		target = "this repository"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "gh pr create does not satisfy the PR rules for %s. Fix the command and re-run:\n", target)
	for _, f := range fixes {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	return b.String()
}
