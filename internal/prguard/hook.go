// Package prguard implements a Claude Code PreToolUse hook. It parses `gh pr create` in Bash
// commands and denies the call with fix instructions when it violates the per-repository rules.
package prguard

import (
	"encoding/json"
	"fmt"
	"io"
)

type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	Cwd           string `json:"cwd"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// Run reads a hook input from stdin and writes a deny decision to stdout when a `gh pr create` command violates
// the rules or the config cannot be loaded.
func Run(stdin io.Reader, stdout io.Writer) error {
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return err
	}
	if in.HookEventName != "PreToolUse" {
		return nil
	}
	pr := parsePRCreate(in.ToolInput.Command)
	if pr == nil || pr.help || pr.dryRun {
		return nil
	}

	reason, err := check(in.Cwd, pr)
	if err != nil {
		// Exiting non-zero would let the command through, so fail closed with a deny.
		reason = fmt.Sprintf("cc-prguard blocked this command because it could not load its config: %v\n"+
			"Ask the user to fix the config; do not work around this check.", err)
	}
	if reason == "" {
		return nil
	}

	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	return json.NewEncoder(stdout).Encode(out)
}

func check(cwd string, pr *prCreate) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", fmt.Errorf("locate config: %w", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return "", err
	}
	repo := pr.repo
	if repo == "" {
		repo = resolveRepo(pr.dir(cwd))
	}
	return cfg.check(repo, pr), nil
}
