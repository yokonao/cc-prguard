package prguard

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGit(dir string, args ...string) error {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run()
}

func hookJSON(t *testing.T, event, cwd, cmd string) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": event,
		"cwd":             cwd,
		"tool_input":      map[string]any{"command": cmd},
	})
	require.NoError(t, err)
	return bytes.NewReader(b)
}

func TestRun(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	require.NoError(t, os.MkdirAll(filepath.Join(cfgHome, "cc-prguard"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cfgHome, "cc-prguard", "config.yaml"), []byte(testConfig), 0o600))
	require.NoError(t, runGit(cfgHome, "init", "-q")) // reuse cfgHome as the working directory
	require.NoError(t, runGit(cfgHome, "remote", "add", "origin", "git@github.com:acme/api.git"))

	t.Run("violation returns deny", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, Run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create -d`), &out))
		var got hookOutput
		require.NoError(t, json.Unmarshal(out.Bytes(), &got))
		assert.Equal(t, "deny", got.HookSpecificOutput.PermissionDecision)
		assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, "acme/api")
		assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, "--milestone")
	})
	t.Run("no output when satisfied", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, Run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create -d -m Q3 -l type:1`), &out))
		assert.Empty(t, out.String())
	})
	t.Run("ignores events other than PreToolUse", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, Run(hookJSON(t, "PostToolUse", cfgHome, `gh pr create`), &out))
		assert.Empty(t, out.String())
	})
	t.Run("ignores --help", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, Run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create --help`), &out))
		assert.Empty(t, out.String())
	})
	t.Run("invalid JSON is an error", func(t *testing.T) {
		assert.Error(t, Run(bytes.NewReader([]byte("{")), &bytes.Buffer{}))
	})
}

func TestRunConfigError(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, cfgPath string)
		want  string
	}{
		{
			"invalid YAML",
			func(t *testing.T, p string) { require.NoError(t, os.WriteFile(p, []byte("rules: ["), 0o600)) },
			"parse ",
		},
		{
			"invalid rule",
			func(t *testing.T, p string) {
				require.NoError(t, os.WriteFile(p, []byte("rules:\n  - require-labels:\n      - hint: x\n"), 0o600))
			},
			"one-of is empty",
		},
		{
			"unreadable",
			func(t *testing.T, p string) { require.NoError(t, os.Mkdir(p, 0o700)) },
			"is a directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgHome := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", cfgHome)
			require.NoError(t, os.MkdirAll(filepath.Join(cfgHome, "cc-prguard"), 0o700))
			cfgPath := filepath.Join(cfgHome, "cc-prguard", "config.yaml")
			tt.setup(t, cfgPath)

			var out bytes.Buffer
			require.NoError(t, Run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create -d`), &out))
			var got hookOutput
			require.NoError(t, json.Unmarshal(out.Bytes(), &got))
			assert.Equal(t, "deny", got.HookSpecificOutput.PermissionDecision)
			assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, cfgPath)
			assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, tt.want)

			out.Reset()
			require.NoError(t, Run(hookJSON(t, "PreToolUse", cfgHome, `gh pr view 1`), &out))
			assert.Empty(t, out.String(), "non-PR commands stay allowed")
		})
	}
}

func TestRunCommands(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	require.NoError(t, os.MkdirAll(filepath.Join(cfgHome, "cc-prguard"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cfgHome, "cc-prguard", "config.yaml"), []byte(testConfig), 0o600))
	base := t.TempDir()
	for name, url := range map[string]string{"api": "git@github.com:acme/api.git", "other": "git@github.com:other/x.git"} {
		dir := filepath.Join(base, name)
		require.NoError(t, os.Mkdir(dir, 0o700))
		require.NoError(t, runGit(dir, "init", "-q"))
		require.NoError(t, runGit(dir, "remote", "add", "origin", url))
	}
	cwd := filepath.Join(base, "other")

	tests := []struct {
		name string
		cmd  string
		deny []string // substrings of the deny reason; nil means allowed
	}{
		{name: "satisfied", cmd: `gh pr create -d`},
		{name: "unrelated command", cmd: `ls -la`},
		{name: "after a newline", cmd: "echo a\ngh pr create -t x -b y", deny: []string{"other/x", "--draft"}},
		{name: "in a subshell", cmd: `(gh pr create -t x -b y)`, deny: []string{"--draft"}},
		{name: "in a block", cmd: `{ gh pr create -t x -b y; }`, deny: []string{"--draft"}},
		{name: "after command", cmd: `command gh pr create -t x`, deny: []string{"--draft"}},
		{name: "after env", cmd: `env gh pr create -t x`, deny: []string{"--draft"}},
		{name: "after time", cmd: `time gh pr create -t x`, deny: []string{"--draft"}},
		{name: "operator without spaces", cmd: `echo a&&gh pr create -t x`, deny: []string{"--draft"}},
		{name: "in a command substitution", cmd: `x=$(gh pr create -t x)`, deny: []string{"--draft"}},
		{name: "in bash -c", cmd: `bash -c "gh pr create -t x"`, deny: []string{"--draft"}},
		{name: "--help is allowed", cmd: `(gh pr create --help)`},
		{name: "--dry-run is allowed", cmd: `bash -c 'gh pr create --dry-run'`},

		{name: "cd before &&", cmd: `cd ../api && gh pr create -d -l type:1`, deny: []string{"acme/api", "--milestone"}},
		{name: "cd before ;", cmd: `cd ../api; gh pr create -d -l type:1`, deny: []string{"acme/api", "--milestone"}},
		{name: "cd in the same subshell", cmd: `(cd ../api && gh pr create -d -l type:1)`, deny: []string{"acme/api", "--milestone"}},
		{name: "builtin cd", cmd: `builtin cd ../api && gh pr create -d -l type:1`, deny: []string{"acme/api", "--milestone"}},
		{name: "cd in another subshell", cmd: `(cd ../api) && gh pr create -d`},
		{name: "cd satisfied", cmd: `cd ../api; gh pr create -d -l type:1 -m Q3`},
		{name: "cd to a variable with repo rules", cmd: `cd "$D" && gh pr create -d`, deny: []string{"a preceding cd"}},
		{name: "cd to a variable with -R", cmd: `cd "$D" && gh pr create -d -R other/x`},

		{name: "every invocation is checked", cmd: `gh pr create -d; gh pr create -d -R acme/y`, deny: []string{"acme/y", "--label"}},
		{name: "every invocation satisfied", cmd: `gh pr create -d && (cd ../api && gh pr create -d -l type:0 -m Q3)`},

		{name: "eval", cmd: `eval "gh pr create -d"`, deny: []string{"uses eval", "plain command"}},
		{name: "variable command name", cmd: `$GH pr create -d`, deny: []string{"command name", "plain command"}},
		{name: "unparsable", cmd: `gh pr create -d -t "x`, deny: []string{"could not be parsed"}},
		{name: "eval of something else", cmd: `eval "$X"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, Run(hookJSON(t, "PreToolUse", cwd, tt.cmd), &out))
			if tt.deny == nil {
				assert.Empty(t, out.String())
				return
			}
			var got hookOutput
			require.NoError(t, json.Unmarshal(out.Bytes(), &got))
			assert.Equal(t, "deny", got.HookSpecificOutput.PermissionDecision)
			for _, s := range tt.deny {
				assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, s)
			}
		})
	}
}

func TestRunWithoutRules(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	require.NoError(t, Run(hookJSON(t, "PreToolUse", t.TempDir(), `eval "gh pr create"`), &out))
	assert.Empty(t, out.String(), "commands that cannot be inspected are allowed without rules")
}
