# Changelog

## v0.2.0 - 2026-10-15

- `gh pr create` is now denied when the config cannot be read or parsed. Previously the hook exited with an error, which Claude Code treats as non-blocking, so the command went through.
- README: recommend `"onFailure": "block"` in the hook config.

## v0.1.0 - 2026-10-03

Initial release.
