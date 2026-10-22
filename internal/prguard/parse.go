package prguard

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// prCreate holds the parts of a `gh pr create` invocation that rules check.
type prCreate struct {
	repo       string // owner/repo from -R / --repo
	cdDir      string // directory set by preceding cd commands, relative to the hook cwd unless absolute
	dirUnknown bool   // a preceding cd changed to a directory that is not statically known
	labels     []string
	milestone  string
	draft      bool
	dryRun     bool
	help       bool
	web        bool
}

// analysis is the result of statically analyzing a Bash command.
type analysis struct {
	prs []*prCreate
	// unknown explains why part of a command that looks like it creates a PR could not be inspected.
	unknown string
}

// prCreateText gates analysis.unknown so that commands that cannot be inspected are denied only when they look
// like they create a PR.
var prCreateText = regexp.MustCompile(`(?s)\bpr\b.*\b(create|new)\b`)

// gh pr create flags that take a value.
var valueFlags = []string{
	"-a", "--assignee", "-B", "--base", "-b", "--body", "-F", "--body-file", "-H", "--head",
	"-p", "--project", "--recover", "-r", "--reviewer", "-T", "--template", "-t", "--title",
}

// parseCommand finds every `gh pr create/new` that cmd runs, looking into lists, pipelines, subshells,
// command substitutions, wrapper commands like env, and `bash -c` scripts.
func parseCommand(cmd string) analysis {
	w := &walker{}
	w.script(cmd, &shellState{})
	if !prCreateText.MatchString(cmd) {
		w.unknown = ""
	}
	return analysis{prs: w.prs, unknown: w.unknown}
}

// shellState is the part of the shell state that affects which repository gh targets.
type shellState struct {
	dir        string
	dirUnknown bool
}

func (st *shellState) cd(dir string) {
	if filepath.IsAbs(dir) {
		st.dir, st.dirUnknown = dir, false
		return
	}
	st.dir = filepath.Join(st.dir, dir)
}

func subshell(st *shellState) *shellState {
	cp := *st
	return &cp
}

type walker struct {
	prs     []*prCreate
	unknown string
}

func (w *walker) fail(reason string) {
	if w.unknown == "" {
		w.unknown = reason
	}
}

func (w *walker) script(src string, st *shellState) {
	f, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		w.fail("it could not be parsed as a shell command")
		return
	}
	w.stmts(f.Stmts, st)
}

func (w *walker) stmts(stmts []*syntax.Stmt, st *shellState) {
	for _, s := range stmts {
		w.stmt(s, st)
	}
}

func (w *walker) stmt(s *syntax.Stmt, st *shellState) {
	if s.Background || s.Coprocess || s.Disown {
		st = subshell(st)
	}
	for _, r := range s.Redirs {
		w.nested(r, st)
	}
	if s.Cmd != nil {
		w.command(s.Cmd, st)
	}
}

func (w *walker) command(c syntax.Command, st *shellState) {
	switch c := c.(type) {
	case *syntax.CallExpr:
		w.call(c, st)
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			w.stmt(c.X, subshell(st))
			w.stmt(c.Y, subshell(st))
			return
		}
		w.stmt(c.X, st)
		w.stmt(c.Y, st)
	case *syntax.Subshell:
		w.stmts(c.Stmts, subshell(st))
	case *syntax.Block:
		w.stmts(c.Stmts, st)
	case *syntax.IfClause:
		for ; c != nil; c = c.Else {
			w.stmts(c.Cond, st)
			w.stmts(c.Then, st)
		}
	case *syntax.WhileClause:
		w.stmts(c.Cond, st)
		w.stmts(c.Do, st)
	case *syntax.ForClause:
		w.nested(c.Loop, st)
		w.stmts(c.Do, st)
	case *syntax.CaseClause:
		w.nested(c.Word, st)
		for _, item := range c.Items {
			for _, p := range item.Patterns {
				w.nested(p, st)
			}
			w.stmts(item.Stmts, st)
		}
	case *syntax.FuncDecl:
		w.stmt(c.Body, subshell(st))
	case *syntax.TimeClause:
		if c.Stmt != nil {
			w.stmt(c.Stmt, st)
		}
	case *syntax.CoprocClause:
		w.stmt(c.Stmt, subshell(st))
	default:
		w.nested(c, st)
	}
}

// nested walks the command substitutions inside n, which run in subshells.
func (w *walker) nested(n syntax.Node, st *shellState) {
	syntax.Walk(n, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CmdSubst:
			w.stmts(n.Stmts, subshell(st))
			return false
		case *syntax.ProcSubst:
			w.stmts(n.Stmts, subshell(st))
			return false
		}
		return true
	})
}

func (w *walker) call(c *syntax.CallExpr, st *shellState) {
	for _, a := range c.Assigns {
		w.nested(a, st)
	}
	toks := make([]token, len(c.Args))
	for i, a := range c.Args {
		w.nested(a, st)
		toks[i] = wordToken(a)
	}
	w.run(toks, st)
}

// run inspects a simple command, unwrapping commands that run their arguments as another command.
func (w *walker) run(toks []token, st *shellState) {
	for len(toks) > 0 {
		if !toks[0].known {
			w.fail("a command name is not a literal word")
			return
		}
		args := toks[1:]
		switch filepath.Base(toks[0].val) {
		case "command":
			for len(args) > 0 && args[0].known && strings.HasPrefix(args[0].val, "-") && args[0].val != "--" {
				if strings.ContainsAny(args[0].val, "vV") {
					return // only looks the command up
				}
				args = args[1:]
			}
			toks = skipOptions(args)
		case "builtin", "nohup":
			toks = skipOptions(args)
		case "exec":
			toks = skipOptions(args, "-a")
		case "time":
			toks = skipOptions(args, "-o", "-f", "--output", "--format")
		case "env":
			toks = w.env(args, &st)
		case "eval":
			w.fail("it uses eval")
			return
		case "cd", "pushd":
			cd(skipOptions(args), st)
			return
		case "popd":
			st.dirUnknown = true
			return
		case "bash", "sh", "zsh", "dash", "ksh", "mksh":
			w.shell(args, st)
			return
		case "gh":
			w.gh(args, st)
			return
		default:
			return
		}
	}
}

func cd(args []token, st *shellState) {
	switch {
	case len(args) == 0:
		home, err := os.UserHomeDir()
		if err != nil {
			st.dirUnknown = true
			return
		}
		st.cd(home)
	case !args[0].known || args[0].val == "-":
		st.dirUnknown = true
	default:
		st.cd(args[0].val)
	}
}

// env skips env's options and assignments. -C changes the directory only for the command env runs, so it
// replaces *st with a modified copy.
func (w *walker) env(args []token, st **shellState) []token {
	for ; len(args) > 0; args = args[1:] {
		a := args[0]
		switch {
		case !a.known:
			if !strings.Contains(a.val, "=") {
				return args
			}
		case a.val == "--":
			return args[1:]
		case a.val == "-S" || strings.HasPrefix(a.val, "--split-string"):
			w.fail("it uses env -S")
			return nil
		case a.val == "-u" || a.val == "--unset":
			if len(args) < 2 {
				return nil
			}
			args = args[1:]
		case a.val == "-C" || a.val == "--chdir" || strings.HasPrefix(a.val, "--chdir="):
			dir, ok := strings.CutPrefix(a.val, "--chdir=")
			known := true
			if !ok {
				if len(args) < 2 {
					return nil
				}
				args = args[1:]
				dir, known = args[0].val, args[0].known
			}
			*st = subshell(*st)
			if known {
				(*st).cd(dir)
			} else {
				(*st).dirUnknown = true
			}
		case strings.HasPrefix(a.val, "-") || strings.Contains(a.val, "="):
		default:
			return args
		}
	}
	return nil
}

// shell inspects `bash -c <script>`. A shell reading its script from stdin cannot be inspected.
func (w *walker) shell(args []token, st *shellState) {
	cmdString, stdin := false, false
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if !a.known {
			w.fail("a shell argument is not a literal word")
			return
		}
		if a.val == "--" || a.val == "-" {
			i++
			break
		}
		if len(a.val) < 2 || (a.val[0] != '-' && a.val[0] != '+') {
			break
		}
		if strings.HasPrefix(a.val, "--") {
			if a.val == "--rcfile" || a.val == "--init-file" {
				i++
			}
			continue
		}
		if a.val[0] == '-' {
			cmdString = cmdString || strings.Contains(a.val, "c")
			stdin = stdin || strings.Contains(a.val, "s")
		}
		if strings.ContainsAny(a.val, "oO") {
			i++ // -o option
		}
	}
	switch {
	case cmdString && i < len(args):
		if !args[i].known {
			w.fail("a shell -c script is not a literal word")
			return
		}
		w.script(args[i].val, subshell(st))
	case cmdString:
	case stdin || i >= len(args):
		w.fail("a shell reads commands from stdin")
	}
}

func (w *walker) gh(args []token, st *shellState) {
	pr := &prCreate{cdDir: st.dir, dirUnknown: st.dirUnknown}
	// Find `pr create`, accepting -R before either subcommand.
	k := 0
	for _, sub := range [][]string{{"pr"}, {"create", "new"}} {
		for ; k < len(args); k++ {
			if v, ok := flagValue(args, &k, "-R", "--repo"); ok {
				if !v.known {
					w.fail("a --repo value is not a literal word")
					return
				}
				pr.repo = normalizeRepo(v.val)
				continue
			}
			if !args[k].known {
				w.fail("a gh subcommand is not a literal word")
				return
			}
			if !strings.HasPrefix(args[k].val, "-") {
				break
			}
		}
		if k >= len(args) || !slices.Contains(sub, args[k].val) {
			return
		}
		k++
	}

	for j := k; j < len(args); j++ {
		a := args[j]
		if a.known && a.val == "--" {
			break
		}
		if v, ok := flagValue(args, &j, "-l", "--label"); ok {
			if !v.known {
				w.fail("a --label value is not a literal word")
				return
			}
			pr.labels = append(pr.labels, splitCSV(v.val)...)
			continue
		}
		if v, ok := flagValue(args, &j, "-m", "--milestone"); ok {
			pr.milestone = v.val
			if !v.known {
				pr.milestone = "(non-literal)"
			}
			continue
		}
		if v, ok := flagValue(args, &j, "-R", "--repo"); ok {
			if !v.known {
				w.fail("a --repo value is not a literal word")
				return
			}
			pr.repo = normalizeRepo(v.val)
			continue
		}
		if !a.known {
			if name, _, ok := strings.Cut(a.val, "="); ok && slices.Contains(valueFlags, name) {
				continue
			}
			w.fail("a gh pr create flag is not a literal word")
			return
		}
		switch a.val {
		case "-d", "--draft", "--draft=true":
			pr.draft = true
		case "--dry-run":
			pr.dryRun = true
		case "-h", "--help":
			pr.help = true
		case "-w", "--web":
			pr.web = true
		default:
			if slices.Contains(valueFlags, a.val) {
				j++ // skip the value even if it looks like a flag
			}
		}
	}
	w.prs = append(w.prs, pr)
}

// token is a shell word. For a word that is not statically known, val is its literal prefix.
type token struct {
	val   string
	known bool
}

func wordToken(w *syntax.Word) token {
	prefix, static := staticPrefix(w.Parts)
	s, err := literal(&syntax.Word{Parts: prefix})
	return token{val: s, known: static && err == nil}
}

// staticPrefix returns the leading parts that are statically known, splitting a double-quoted part if needed, and
// whether that is all of parts.
func staticPrefix(parts []syntax.WordPart) ([]syntax.WordPart, bool) {
	for i, p := range parts {
		switch p := p.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
			continue
		case *syntax.DblQuoted:
			inner, static := staticPrefix(p.Parts)
			if static {
				continue
			}
			return append(slices.Clip(parts[:i]), &syntax.DblQuoted{Parts: inner}), false
		}
		return parts[:i], false
	}
	return parts, true
}

// literal expands quotes, escapes, and a leading ~ of a word made of static parts.
func literal(w *syntax.Word) (string, error) {
	cfg := &expand.Config{}
	if home, err := os.UserHomeDir(); err == nil {
		cfg.Env = expand.ListEnviron("HOME=" + home)
	}
	return expand.Literal(cfg, w)
}

// skipOptions drops the leading options of a wrapper command. Options in withValue take the next argument.
func skipOptions(args []token, withValue ...string) []token {
	for len(args) > 0 && args[0].known && strings.HasPrefix(args[0].val, "-") && args[0].val != "-" {
		a := args[0].val
		args = args[1:]
		if a == "--" {
			break
		}
		if slices.Contains(withValue, a) && len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

// flagValue returns the value if toks[*i] is the short or long flag, advancing *i for the `flag value` form.
func flagValue(toks []token, i *int, short, long string) (token, bool) {
	a := toks[*i]
	if v, ok := strings.CutPrefix(a.val, long+"="); ok {
		return token{val: v, known: a.known}, true
	}
	if !a.known || (a.val != short && a.val != long) {
		return token{}, false
	}
	if *i+1 >= len(toks) {
		return token{known: true}, true
	}
	*i++
	return toks[*i], true
}

// dir returns the directory gh runs in.
func (pr *prCreate) dir(cwd string) string {
	switch {
	case pr.cdDir == "":
		return cwd
	case filepath.IsAbs(pr.cdDir):
		return pr.cdDir
	default:
		return filepath.Join(cwd, pr.cdDir)
	}
}

func splitCSV(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
