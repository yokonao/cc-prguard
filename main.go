// cc-prguard is a Claude Code PreToolUse hook. It parses `gh pr create` in Bash commands
// and denies the call with fix instructions when it violates the per-repository rules.
// Claude Code treats exit 2 as a blocking error, so failures exit with 1.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cc-prguard:", err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
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

	path, err := configPath()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return err
	}
	repo := pr.repo
	if repo == "" {
		repo = resolveRepo(pr.dir(in.Cwd))
	}
	reason := cfg.check(repo, pr)
	if reason == "" {
		return nil
	}

	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	return json.NewEncoder(stdout).Encode(out)
}
