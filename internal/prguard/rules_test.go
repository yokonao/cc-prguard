package prguard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testConfig = `
rules:
  - require-draft: true
    forbid-web: true
  - repos: ["acme/*"]
    require-labels:
      - one-of: [type:0, type:1]
        hint: 0=bug/1=feature
  - repos: ["acme/api"]
    require-milestone: true
    require-draft: true
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestCheck(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, testConfig))
	require.NoError(t, err)

	tests := []struct {
		name     string
		repo     string
		cmd      string
		contains []string
		absent   []string
	}{
		{name: "satisfies every rule", repo: "acme/api", cmd: `gh pr create -d -l type:1 -m Q3`},
		{name: "rule without repos applies to every repo", repo: "other/repo", cmd: `gh pr create`, contains: []string{"other/repo", "--draft"}},
		{name: "repo not matching the glob is skipped", repo: "other/repo", cmd: `gh pr create -d`},
		{
			name:     "lists every missing requirement once",
			repo:     "acme/api",
			cmd:      `gh pr create --web -l type:9`,
			contains: []string{"remove --web", "add --draft", "add --milestone", "one of: type:0, type:1 (0=bug/1=feature)"},
		},
		{name: "omits satisfied requirements", repo: "acme/x", cmd: `gh pr create -l type:1`, contains: []string{"--draft"}, absent: []string{"--label"}},
		{name: "unresolved repo skips rules with repos", cmd: `gh pr create -d`},
		{name: "unresolved repo still gets global rules", cmd: `gh pr create`, contains: []string{"this repository", "--draft"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.check(tt.repo, parsePRCreate(tt.cmd))
			if len(tt.contains) == 0 {
				assert.Empty(t, got)
				return
			}
			for _, s := range tt.contains {
				assert.Contains(t, got, s)
			}
			for _, s := range tt.absent {
				assert.NotContains(t, got, s)
			}
		})
	}

	t.Run("deduplicates requirements", func(t *testing.T) {
		got := cfg.check("acme/api", parsePRCreate(`gh pr create -l type:0 -m Q3`))
		assert.Equal(t, "gh pr create does not satisfy the PR rules for acme/api. Fix the command and re-run:\n  - add --draft\n", got)
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("missing file means no rules", func(t *testing.T) {
		cfg, err := loadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
		require.NoError(t, err)
		assert.Empty(t, cfg.Rules)
	})
	t.Run("empty one-of is an error", func(t *testing.T) {
		_, err := loadConfig(writeConfig(t, "rules:\n  - require-labels:\n      - hint: x\n"))
		assert.ErrorContains(t, err, "rules[0].require-labels[0]")
	})
}
