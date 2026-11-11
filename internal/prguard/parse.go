package prguard

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

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
	if !w.always && !prCreateText.MatchString(strings.NewReplacer(`\`, "", `'`, "", `"`, "").Replace(cmd)) {
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
	always  bool // unknown is kept even if the command text does not look like it creates a PR
}

func (w *walker) fail(reason string) {
	if w.unknown == "" {
		w.unknown = reason
	}
}

func (w *walker) failAlways(reason string) {
	if w.unknown == "" {
		w.unknown, w.always = reason, true
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
	w.run(toks, st, false)
}

// run inspects a simple command, unwrapping commands that run their arguments as another command. fromStdin
// means a wrapper appends arguments assembled at run time.
func (w *walker) run(toks []token, st *shellState, fromStdin bool) {
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
		case "timeout":
			if toks = getopt(args, "sk", []string{"--signal", "--kill-after"}, nil); len(toks) > 0 {
				toks = toks[1:] // duration
			}
		case "nice":
			toks = getopt(args, "n", []string{"--adjustment"}, nil)
		case "stdbuf":
			toks = getopt(args, "ioe", []string{"--input", "--output", "--error"}, nil)
		case "caffeinate":
			toks = getopt(args, "tw", nil, nil)
		case "sudo":
			toks = w.sudo(args, &st)
		case "arch":
			toks = skipOptions(args, "-arch", "-d", "-e")
		case "script":
			toks = w.scriptCmd(args, st)
		case "watch":
			toks = w.watch(args, st)
		case "mise":
			toks = w.mise(args, &st)
		case "find":
			w.find(args, st)
			return
		case "parallel":
			w.parallel(args, st)
			return
		case "xargs":
			fromStdin = true
			toks = getopt(args, "aEdILnPsJRS", []string{
				"--arg-file", "--delimiter", "--max-lines", "--max-args", "--max-procs", "--max-chars", "--process-slot-var",
			}, nil)
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
			w.gh(args, st, fromStdin)
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

// sudo skips sudo's options and assignments. -D changes the directory only for the command sudo runs.
func (w *walker) sudo(args []token, st **shellState) []token {
	args = getopt(args, "aCcDgpRrTtUu", []string{
		"--auth-type", "--close-from", "--login-class", "--chdir", "--group", "--prompt", "--chroot", "--role",
		"--command-timeout", "--type", "--other-user", "--user",
	}, func(opt string, v token) {
		if opt != "-D" && opt != "--chdir" {
			return
		}
		*st = subshell(*st)
		if v.known {
			(*st).cd(v.val)
		} else {
			(*st).dirUnknown = true
		}
	})
	for len(args) > 0 && shellAssignment.MatchString(args[0].val) {
		args = args[1:]
	}
	return args
}

var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// shellString inspects a command string that a wrapper runs in a shell.
func (w *walker) shellString(v token, st *shellState) {
	if !v.known {
		w.fail("a shell command string is not a literal word")
		return
	}
	w.script(v.val, subshell(st))
}

// scriptCmd skips the options and file of script(1), covering both the BSD and util-linux options. util-linux
// script runs -c in a shell.
func (w *walker) scriptCmd(args []token, st *shellState) []token {
	args = getopt(args, "BcEImOtT", []string{
		"--log-io", "--command", "--echo", "--log-in", "--logging-format", "--log-out", "--log-timing",
	}, func(opt string, v token) {
		if opt == "-c" || opt == "--command" {
			w.shellString(v, st)
		}
	})
	if len(args) == 0 {
		return nil
	}
	return args[1:]
}

// watch returns the command that `watch -x` runs. Without -x, watch runs its arguments joined by spaces with
// sh -c, so watch inspects them as a script.
func (w *walker) watch(args []token, st *shellState) []token {
	cmd := getopt(args, "nq", []string{"--interval", "--equexit"}, nil)
	for _, a := range args[:len(args)-len(cmd)] {
		if a.val == "--exec" {
			return cmd
		}
		if short, ok := strings.CutPrefix(a.val, "-"); ok && !strings.HasPrefix(short, "-") {
			if i := strings.IndexAny(short, "nq"); i >= 0 {
				short = short[:i] // the rest is the option value
			}
			if strings.Contains(short, "x") {
				return cmd
			}
		}
	}
	vals := make([]string, len(cmd))
	for i, a := range cmd {
		if !a.known {
			w.fail("a watch argument is not a literal word")
			return nil
		}
		vals[i] = a.val
	}
	w.script(strings.Join(vals, " "), subshell(st))
	return nil
}

// mise returns the command after -- of `mise exec` / `mise x` and inspects its -c shell command. -C changes the
// directory only for that command.
func (w *walker) mise(args []token, st **shellState) []token {
	exec := false
	for i := 0; i < len(args); i++ {
		if v, ok := flagValue(args, &i, "-C", "--cd"); ok {
			*st = subshell(*st)
			if v.known {
				(*st).cd(v.val)
			} else {
				(*st).dirUnknown = true
			}
			continue
		}
		if v, ok := flagValue(args, &i, "-c", "--command"); ok {
			w.shellString(v, *st)
			continue
		}
		switch a := args[i]; {
		case !a.known:
		case a.val == "--":
			if !exec {
				return nil
			}
			return args[i+1:]
		case a.val == "exec" || a.val == "x":
			exec = true
		}
	}
	return nil
}

// find inspects the commands of -exec, -execdir, -ok, and -okdir, which get the found paths as arguments.
func (w *walker) find(args []token, st *shellState) {
	for i := 0; i < len(args); i++ {
		if !args[i].known || !slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, args[i].val) {
			continue
		}
		var cmd []token
		for i++; i < len(args); i++ {
			a := args[i]
			if a.known && (a.val == ";" || a.val == "+" && args[i-1].val == "{}") {
				break
			}
			cmd = append(cmd, placeholder(a, "{}"))
		}
		w.run(cmd, st, true)
	}
}

// parallel inspects gh in the command that GNU parallel runs. Its arguments are assembled at run time, and a
// command given as a single word or as an input after ::: runs in a shell, which is denied if it mentions gh.
func (w *walker) parallel(args []token, st *shellState) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !a.known:
			w.fail("a parallel argument is not a literal word")
		case filepath.Base(a.val) == "gh":
			end := i + 1
			for end < len(args) && (!args[end].known || !strings.HasPrefix(args[end].val, ":::")) {
				end++
			}
			cmd := make([]token, 0, end-i)
			for _, t := range args[i:end] {
				cmd = append(cmd, placeholder(t, "{"))
			}
			w.run(cmd, st, true)
			i = end
		case ghWord.MatchString(a.val):
			w.failAlways("parallel runs gh in a shell command line")
			return
		}
	}
}

var ghWord = regexp.MustCompile(`(^|[\s;&|(/'"])gh(\s|$)`)

// placeholder turns a word that contains a replacement string such as {} into an unknown token.
func placeholder(t token, marker string) token {
	if i := strings.Index(t.val, marker); i >= 0 {
		return token{val: t.val[:i]}
	}
	return t
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

// gh records a `gh pr create`. With fromStdin, more arguments are assembled at run time, so gh is denied unless
// its literal arguments already name another subcommand.
func (w *walker) gh(args []token, st *shellState, fromStdin bool) {
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
				if fromStdin {
					w.failAlways("gh runs with arguments assembled at run time")
					return
				}
				w.fail("a gh subcommand is not a literal word")
				return
			}
			if !strings.HasPrefix(args[k].val, "-") {
				break
			}
		}
		if k >= len(args) && fromStdin {
			w.failAlways("gh runs with arguments assembled at run time")
			return
		}
		if k >= len(args) || !slices.Contains(sub, args[k].val) {
			return
		}
		k++
	}
	if fromStdin {
		w.failAlways("gh runs with arguments assembled at run time")
		return
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
	prefix = unescape(prefix)
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

// unescape turns each backslash-escaped character in unquoted literals into a single-quoted part, because
// expand.Literal keeps the backslashes of unquoted literals.
func unescape(parts []syntax.WordPart) []syntax.WordPart {
	var out []syntax.WordPart
	for _, p := range parts {
		lit, ok := p.(*syntax.Lit)
		if !ok || !strings.Contains(lit.Value, `\`) {
			out = append(out, p)
			continue
		}
		s := lit.Value
		for {
			i := strings.IndexByte(s, '\\')
			if i < 0 || i == len(s)-1 {
				break // a trailing backslash is kept as-is
			}
			_, size := utf8.DecodeRuneInString(s[i+1:])
			if i > 0 {
				out = append(out, &syntax.Lit{Value: s[:i]})
			}
			out = append(out, &syntax.SglQuoted{Value: s[i+1 : i+1+size]})
			s = s[i+1+size:]
		}
		if s != "" {
			out = append(out, &syntax.Lit{Value: s})
		}
	}
	return out
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

// getopt drops the leading getopt-style options of a wrapper command. short lists the short options and long the
// long options that take a value; onValue, if not nil, receives each such option and its value.
func getopt(args []token, short string, long []string, onValue func(opt string, v token)) []token {
	for len(args) > 0 && args[0].known && strings.HasPrefix(args[0].val, "-") && args[0].val != "-" {
		a := args[0].val
		args = args[1:]
		var opt string
		var v token
		switch {
		case a == "--":
			return args
		case strings.HasPrefix(a, "--"):
			name, val, ok := strings.Cut(a, "=")
			if !slices.Contains(long, name) {
				continue
			}
			opt, v = name, token{val: val, known: true}
			if !ok {
				if len(args) == 0 {
					return nil
				}
				v, args = args[0], args[1:]
			}
		default:
			i := strings.IndexAny(a[1:], short)
			if i < 0 {
				continue
			}
			opt, v = "-"+a[1+i:2+i], token{val: a[2+i:], known: true}
			if v.val == "" {
				if len(args) == 0 {
					return nil
				}
				v, args = args[0], args[1:]
			}
		}
		if onValue != nil {
			onValue(opt, v)
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
