package prguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePRCreate(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want *prCreate
	}{
		{"not create", `gh pr view 1`, nil},
		{"not gh", `echo gh pr create`, nil},
		{"gh after an operator and env assignment", `true && GH_HOST=github.com gh pr create -d`, &prCreate{draft: true}},
		{"no flags", `gh pr create`, &prCreate{}},
		{"new alias and absolute path", `/opt/homebrew/bin/gh pr new -d`, &prCreate{draft: true}},
		{
			"label and milestone",
			`gh pr create --label "type:3,feature request" -l wip --milestone=Q3`,
			&prCreate{labels: []string{"type:3", "feature request", "wip"}, milestone: "Q3"},
		},
		{"ignores flag-like values", `gh pr create --body "--draft --web" --title -w`, &prCreate{}},
		{"ignores flags of the next command", `gh pr create --title x && echo --draft`, &prCreate{}},
		{"global -R", `gh -R acme/example pr create --draft`, &prCreate{repo: "acme/example", draft: true}},
		{"--repo as URL", `gh pr create --repo https://github.com/acme/example.git`, &prCreate{repo: "acme/example"}},
		{"preceding cd", `cd ../other && gh pr create --web`, &prCreate{cdDir: "../other", web: true}},
		{"help and dry-run", `gh pr create --help --dry-run`, &prCreate{help: true, dryRun: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parsePRCreate(tt.cmd))
		})
	}
}

func TestPRCreateDir(t *testing.T) {
	assert.Equal(t, "/w", (&prCreate{}).dir("/w"))
	assert.Equal(t, "/x", (&prCreate{cdDir: "/x"}).dir("/w"))
	assert.Equal(t, "/other", (&prCreate{cdDir: "../other"}).dir("/w"))
}

func TestNormalizeRepo(t *testing.T) {
	for in, want := range map[string]string{
		"acme/api":                         "acme/api",
		"github.com/acme/api":              "acme/api",
		"git@github.com:acme/api.git":      "acme/api",
		"https://github.com/acme/api.git/": "acme/api",
		"ssh://git@github.com/acme/api":    "acme/api",
		"api":                              "",
	} {
		assert.Equal(t, want, normalizeRepo(in), in)
	}
}

func TestResolveRepo(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		require.NoError(t, runGit(dir, args...))
	}
	git("init", "-q")
	assert.Empty(t, resolveRepo(dir), "no remote")

	git("remote", "add", "origin", "git@github.com:me/fork.git")
	assert.Equal(t, "me/fork", resolveRepo(dir))

	git("remote", "add", "upstream", "https://github.com/org/repo.git")
	git("config", "remote.upstream.gh-resolved", "base")
	assert.Equal(t, "org/repo", resolveRepo(dir), "prefers gh repo set-default")
}
