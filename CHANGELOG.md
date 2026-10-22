# Changelog

## v0.3.0 - 2026-10-23

- Parse the Bash command as a shell script instead of splitting it into words. `gh pr create` is now checked after a newline or an operator without spaces, in subshells, blocks, command substitutions, and control flow, behind `command`, `builtin`, `env`, `exec`, `time`, and `nohup`, and in `bash -c` scripts. Previously these were allowed without checks.
- `cd <dir>` is now taken into account after `;` and a newline, inside subshells, and as `builtin cd`.
- Every `gh pr create` in a command is checked, not just the first.
- A command that looks like it creates a PR but cannot be inspected statically (`eval`, `$GH pr create`, a non-literal `--label`, `--repo`, or `cd` target, a shell reading from stdin, or a parse error) is denied with a request to write it plainly.

## v0.2.0 - 2026-10-15

- `gh pr create` is now denied when the config cannot be read or parsed. Previously the hook exited with an error, which Claude Code treats as non-blocking, so the command went through.
- README: recommend `"onFailure": "block"` in the hook config.

## v0.1.0 - 2026-10-03

Initial release.
