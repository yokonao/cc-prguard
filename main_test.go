package main

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
		require.NoError(t, run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create -d`), &out))
		var got hookOutput
		require.NoError(t, json.Unmarshal(out.Bytes(), &got))
		assert.Equal(t, "deny", got.HookSpecificOutput.PermissionDecision)
		assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, "acme/api")
		assert.Contains(t, got.HookSpecificOutput.PermissionDecisionReason, "--milestone")
	})
	t.Run("no output when satisfied", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create -d -m Q3 -l type:1`), &out))
		assert.Empty(t, out.String())
	})
	t.Run("ignores events other than PreToolUse", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, run(hookJSON(t, "PostToolUse", cfgHome, `gh pr create`), &out))
		assert.Empty(t, out.String())
	})
	t.Run("ignores --help", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, run(hookJSON(t, "PreToolUse", cfgHome, `gh pr create --help`), &out))
		assert.Empty(t, out.String())
	})
	t.Run("invalid JSON is an error", func(t *testing.T) {
		assert.Error(t, run(bytes.NewReader([]byte("{")), &bytes.Buffer{}))
	})
}
