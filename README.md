# cc-prguard

A [Claude Code](https://docs.claude.com/en/docs/claude-code) `PreToolUse` hook that checks `gh pr create` commands against per-repository rules.

When a command misses a requirement (draft, labels, milestone), the hook denies it and tells Claude how to fix the command, so the PR is created correctly on the next try.

## Install

```sh
go install github.com/yokonao/cc-prguard@latest
```

## Configure the hook

Add the hook to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "/path/to/cc-prguard" }]
      }
    ]
  }
}
```

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

The target repository is resolved in the same order as `gh`: `-R` / `--repo`, the `gh repo set-default` remote, then `origin`. A preceding `cd <dir> &&` is taken into account. If the repository cannot be resolved, only rules without `repos` apply.

`gh pr create --help` and `--dry-run` are always allowed.

## License

[MIT](LICENSE)
