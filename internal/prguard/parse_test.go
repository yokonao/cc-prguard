package prguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseOne(t *testing.T, cmd string) *prCreate {
	t.Helper()
	an := parseCommand(cmd)
	require.Empty(t, an.unknown)
	require.Len(t, an.prs, 1)
	return an.prs[0]
}

func TestParseCommand(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	draft := []*prCreate{{draft: true}}
	none := []*prCreate{{}}
	tests := []struct {
		name    string
		cmd     string
		want    []*prCreate
		unknown bool
	}{
		{name: "not create", cmd: `gh pr view 1`},
		{name: "not gh", cmd: `echo gh pr create`},
		{name: "gh after an operator and env assignment", cmd: `true && GH_HOST=github.com gh pr create -d`, want: draft},
		{name: "no flags", cmd: `gh pr create`, want: none},
		{name: "new alias and absolute path", cmd: `/opt/homebrew/bin/gh pr new -d`, want: draft},
		{
			name: "label and milestone",
			cmd:  `gh pr create --label "type:3,feature request" -l wip --milestone=Q3`,
			want: []*prCreate{{labels: []string{"type:3", "feature request", "wip"}, milestone: "Q3"}},
		},
		{name: "ignores flag-like values", cmd: `gh pr create --body "--draft --web" --title -w`, want: none},
		{name: "ignores flags of the next command", cmd: `gh pr create --title x && echo --draft`, want: none},
		{name: "global -R", cmd: `gh -R acme/example pr create --draft`, want: []*prCreate{{repo: "acme/example", draft: true}}},
		{name: "-R between subcommands", cmd: `gh pr -R acme/example create`, want: []*prCreate{{repo: "acme/example"}}},
		{name: "--repo as URL", cmd: `gh pr create --repo https://github.com/acme/example.git`, want: []*prCreate{{repo: "acme/example"}}},
		{name: "help and dry-run", cmd: `gh pr create --help --dry-run`, want: []*prCreate{{help: true, dryRun: true}}},
		{name: "quoted words", cmd: `"gh" 'pr' create -l 'a b' -d`, want: []*prCreate{{labels: []string{"a b"}, draft: true}}},

		{name: "after a newline", cmd: "echo a\ngh pr create -t x -b y", want: none},
		{name: "operator without spaces", cmd: `echo a&&gh pr create -t x`, want: none},
		{name: "subshell", cmd: `(gh pr create -t x -b y)`, want: none},
		{name: "block", cmd: `{ gh pr create -t x -b y; }`, want: none},
		{name: "pipeline", cmd: `echo y | gh pr create -d`, want: draft},
		{name: "background", cmd: `gh pr create -d & wait`, want: draft},
		{name: "command substitution in an assignment", cmd: `x=$(gh pr create -t x)`, want: none},
		{name: "command substitution in an argument", cmd: `echo "$(gh pr create -d)"`, want: draft},
		{name: "backquotes", cmd: "echo `gh pr create`", want: none},
		{name: "process substitution", cmd: `cat <(gh pr create)`, want: none},
		{name: "if body", cmd: `if true; then gh pr create; fi`, want: none},
		{name: "else body", cmd: `if false; then :; else gh pr create; fi`, want: none},
		{name: "for body", cmd: `for i in 1; do gh pr create; done`, want: none},
		{name: "while condition", cmd: `while gh pr create; do :; done`, want: none},
		{name: "case body", cmd: `case x in x) gh pr create ;; esac`, want: none},
		{name: "function body", cmd: `f() { gh pr create; }; f`, want: none},
		{name: "every invocation", cmd: `gh pr create -d && gh pr create -R a/b`, want: []*prCreate{{draft: true}, {repo: "a/b"}}},

		{name: "command", cmd: `command gh pr create -t x`, want: none},
		{name: "command -p", cmd: `command -p gh pr create -d`, want: draft},
		{name: "command -v only looks up", cmd: `command -v gh pr create`},
		{name: "env", cmd: `env gh pr create`, want: none},
		{name: "env options and assignments", cmd: `env -i A=1 -u B "C=$D" gh pr create -d`, want: draft},
		{name: "env -C", cmd: `env -C ../other gh pr create && gh pr create -d`, want: []*prCreate{{cdDir: "../other"}, {draft: true}}},
		{name: "time keyword", cmd: `time gh pr create`, want: none},
		{name: "time command", cmd: `/usr/bin/time -o out gh pr create`, want: none},
		{name: "exec", cmd: `exec -a x gh pr create`, want: none},
		{name: "nohup", cmd: `nohup gh pr create`, want: none},
		{name: "nested wrappers", cmd: `nohup env A=1 command gh pr create -d`, want: draft},
		{name: "bash -c", cmd: `bash -c "gh pr create -t x"`, want: none},
		{name: "sh with combined -c", cmd: `/bin/sh -ec 'cd ../other && gh pr create'`, want: []*prCreate{{cdDir: "../other"}}},
		{name: "bash -c does not change the outer directory", cmd: `bash -c 'cd ../other' && gh pr create`, want: none},
		{name: "bash running a script file", cmd: `bash ./create-pr.sh`},

		{name: "preceding cd", cmd: `cd ../other && gh pr create --web`, want: []*prCreate{{cdDir: "../other", web: true}}},
		{name: "cd before ;", cmd: `cd ../other; gh pr create`, want: []*prCreate{{cdDir: "../other"}}},
		{name: "cd before a newline", cmd: "cd ../other\ngh pr create", want: []*prCreate{{cdDir: "../other"}}},
		{name: "cd in the same subshell", cmd: `(cd ../other && gh pr create)`, want: []*prCreate{{cdDir: "../other"}}},
		{name: "cd in another subshell", cmd: `(cd ../other) && gh pr create`, want: none},
		{name: "cd in a block", cmd: `{ cd ../other; } && gh pr create`, want: []*prCreate{{cdDir: "../other"}}},
		{name: "builtin cd", cmd: `builtin cd ../other && gh pr create`, want: []*prCreate{{cdDir: "../other"}}},
		{name: "cd -P", cmd: `cd -P ../other && gh pr create`, want: []*prCreate{{cdDir: "../other"}}},
		{name: "successive cd", cmd: `cd ../a && cd b && gh pr create`, want: []*prCreate{{cdDir: "../a/b"}}},
		{name: "absolute cd", cmd: `cd ../a && cd /src/b && gh pr create`, want: []*prCreate{{cdDir: "/src/b"}}},
		{name: "cd home", cmd: `cd && gh pr create`, want: []*prCreate{{cdDir: "/home/me"}}},
		{name: "cd tilde", cmd: `cd ~/src && gh pr create`, want: []*prCreate{{cdDir: "/home/me/src"}}},
		{name: "cd to a variable", cmd: `cd "$DIR" && gh pr create`, want: []*prCreate{{dirUnknown: true}}},
		{name: "cd -", cmd: `cd - && gh pr create`, want: []*prCreate{{dirUnknown: true}}},
		{name: "absolute cd after an unknown one", cmd: `cd "$DIR" && cd /src && gh pr create`, want: []*prCreate{{cdDir: "/src"}}},

		{name: "dynamic title and body", cmd: "gh pr create -d -t \"$T\" --title=\"$T\" --body \"$(cat <<'EOF'\nbody\nEOF\n)\"", want: draft},
		{name: "dynamic milestone", cmd: `gh pr create -m "$M"`, want: []*prCreate{{milestone: "(non-literal)"}}},

		{name: "eval", cmd: `eval "gh pr create"`, unknown: true},
		{name: "variable command name", cmd: `$GH pr create`, unknown: true},
		{name: "variable wrapped command name", cmd: `command "$GH" pr create`, unknown: true},
		{name: "variable global flags", cmd: `gh $FLAGS pr create`, unknown: true},
		{name: "variable label", cmd: `gh pr create -l "$L"`, unknown: true},
		{name: "variable repo", cmd: `gh pr create --repo="$R"`, unknown: true},
		{name: "variable flags", cmd: `gh pr create "$@"`, unknown: true},
		{name: "variable bash -c script", cmd: `bash -c "$CMD pr create"`, unknown: true},
		{name: "shell reading stdin", cmd: `echo 'gh pr create' | bash`, unknown: true},
		{name: "shell reading a here-string", cmd: `bash <<<'gh pr create'`, unknown: true},
		{name: "env -S", cmd: `env -S 'gh pr create'`, unknown: true},
		{name: "unparsable", cmd: `gh pr create -t "unterminated`, unknown: true},
		{name: "unparsable bash -c script", cmd: `bash -c 'gh pr create -t "x'`, unknown: true},

		{name: "eval of something else", cmd: `eval "$X"`},
		{name: "variable command name of something else", cmd: `"$EDITOR" notes.txt`},
		{name: "unparsable command of something else", cmd: `echo "unterminated`},
		{name: "dynamic gh api call", cmd: `gh api -f q="$Q" graphql`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCommand(tt.cmd)
			assert.Equal(t, tt.want, got.prs)
			assert.Equal(t, tt.unknown, got.unknown != "", got.unknown)
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
