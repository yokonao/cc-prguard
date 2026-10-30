# cc-prguard

A [Claude Code](https://docs.claude.com/en/docs/claude-code) `PreToolUse` hook that checks `gh pr create` commands against per-repository rules.

When a command misses a requirement (draft, labels, milestone), the hook denies it and tells Claude how to fix the command, so the PR is created correctly on the next try.

## Install

Download a prebuilt binary from [GitHub Releases](https://github.com/yokonao/cc-prguard/releases).
Every release ships with a build provenance attestation.

See [docs/install.md](docs/install.md) for other install methods and how to verify a release.

## Configure the hook

Add the hook to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "cc-prguard", "onFailure": "block" }]
      }
    ]
  }
}
```

If the config cannot be read or parsed, cc-prguard denies `gh pr create` with the error and allows other commands.
`"onFailure": "block"` (Claude Code v2.1.295 or later) also blocks the Bash call when the hook itself fails, for example when the binary is missing or crashes.
Since the hook runs on every Bash call, such a failure blocks all of them until it is fixed; without the option, Claude Code lets them through.

In Claude Code v2.1.314 or later, the `Bash` matcher also covers the shell commands the Monitor tool starts, so cc-prguard checks `gh pr create` in them too.
Earlier versions do not run the hook for Monitor.
With `"onFailure": "block"`, a failing hook then blocks Monitor calls as well as Bash calls.

## Rules

Rules live in `$XDG_CONFIG_HOME/cc-prguard/config.yaml` (default `~/.config/cc-prguard/config.yaml`). Without the file, every command is allowed.

```yaml
rules:
  # No repos: applies to every repository.
  - require-draft: true
    forbid-web: true
  # repos accepts owner/repo patterns in path.Match syntax.
  - repos: ["my-org/*"]
    require-labels:
      - one-of: [bug, enhancement, chore]
        hint: classify the change
    require-milestone: true
```

| Key | Requirement |
| --- | --- |
| `repos` | Target repositories. Omit to apply to all. |
| `require-draft` | `--draft` |
| `forbid-web` | No `--web`, since the browser flow bypasses the checks |
| `require-milestone` | `--milestone` |
| `require-labels` | For each entry, at least one `--label` from `one-of`. `hint` is shown with the fix. |

The target repository is resolved in the same order as `gh`: `-R` / `--repo`, the `gh repo set-default` remote, then `origin`. An earlier `cd <dir>` in the same shell (after `&&`, `;`, or a newline, scoped to its subshell) is taken into account. If the repository cannot be resolved, only rules without `repos` apply.

The whole Bash command is parsed, so every `gh pr create` in it is checked: in lists and pipelines, subshells, blocks, command substitutions, `if` / `for` / `while` bodies, behind `command`, `builtin`, `env`, `exec`, `time`, and `nohup`, and in `bash -c` / `sh -c` scripts.
A command that looks like it creates a PR but cannot be inspected statically, such as `eval`, `$GH pr create`, a non-literal `--label` or `cd` target, or a shell reading commands from stdin, is denied with a request to write it plainly.

`gh pr create --help` and `--dry-run` are always allowed.

## Development

```sh
go test ./...
golangci-lint run
golangci-lint fmt
```

## License

[MIT](LICENSE)
