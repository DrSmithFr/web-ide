package agent

import (
	"os"
	"regexp"
	"strings"
)

// Shell commands run without asking. The Plan, Briefing and Orchestrator modes run the
// commands that only read (anywhere), the build, test and lint commands or scripts of the
// project run from inside it (RunsFreely), and anything confined to the scratch folder; the
// Build mode runs anything confined to the project and the scratch folder (StaysIn). Anything
// else (installs, deletions, writes out of the project, unknown programs) waits for the user.
// A guess, kept conservative: what it cannot parse asks.

var readCommands = setOf(
	"ls", "cat", "head", "tail", "less", "more", "grep", "egrep", "fgrep", "rg", "ag", "fd", "wc", "file", "stat", "pwd", "echo", "printf", "tree", "du", "df",
	"which", "whereis", "type", "env", "printenv", "sort", "uniq", "cut", "tr", "diff", "cmp", "comm", "jq", "yq", "basename", "dirname", "realpath", "readlink",
	"date", "whoami", "id", "uname", "hostname", "ps", "nl", "column", "md5sum", "sha1sum", "sha256sum", "true", "false", "test", "[", "awk", "sed", "find",
	"xxd", "hexdump", "od", "strings", "tac", "rev", "paste", "join", "fold", "seq", "sleep", "free", "uptime", "nproc", "export",
	"go", "gofmt", "git", "npm", "node", "python", "python3", "php", "composer", "cargo", "make", "docker",
)

var readSub = map[string]*regexp.Regexp{
	"git":      regexp.MustCompile(`^(status|log|diff|show|blame|ls-files|ls-tree|grep|rev-parse|rev-list|describe|shortlog|reflog|cat-file|merge-base|name-rev|for-each-ref|check-ignore|count-objects|whatchanged|range-diff|config\s+(--get|--list|-l)|remote(\s+-v)?$|branch(\s+(-a|-r|-v|-vv|--list|--show-current|--contains\s+\S+))*$|tag(\s+(-l|--list))?$|stash\s+(list|show)|worktree\s+list)`),
	"go":       regexp.MustCompile(`^(vet|list|doc|version|env)\b`),
	"gofmt":    regexp.MustCompile(`^(-l|-d)\b`),
	"npm":      regexp.MustCompile(`^(ls|list|view|outdated|explain|why|-v|--version)\b`),
	"node":     regexp.MustCompile(`^(-v|--version)$`),
	"python":   regexp.MustCompile(`^(-V|--version)$`),
	"python3":  regexp.MustCompile(`^(-V|--version)$`),
	"php":      regexp.MustCompile(`^(-v|--version|-l)\b`),
	"composer": regexp.MustCompile(`^(show|outdated|licenses|validate|-V|--version)\b`),
	"cargo":    regexp.MustCompile(`^(tree|metadata|--version)\b`),
	"make":     regexp.MustCompile(`^(-n|--dry-run)\b`),
	"docker":   regexp.MustCompile(`^(ps|images|logs|inspect|version|info|compose\s+(ps|logs|config|ls|images))\b`),
}

var anything = regexp.MustCompile(`^`)

// Build, test and lint commands: they may write build outputs in the project, never install,
// publish, deploy or rewrite sources. Run freely from inside the project only.
var projectSub = map[string]*regexp.Regexp{
	"go":       regexp.MustCompile(`^(build|test|vet|run|list|doc|version|env|mod\s+(graph|why|verify))\b`),
	"npm":      regexp.MustCompile(`^(test|t|run|run-script)\b`),
	"pnpm":     regexp.MustCompile(`^(test|t|run|lint|build|check|typecheck|why)\b`),
	"yarn":     regexp.MustCompile(`^(test|run|lint|build|check|typecheck|why)\b`),
	"npx":      regexp.MustCompile(`^(--no-install\s+)?(tsc|vitest|jest|mocha|eslint|playwright\s+test|prettier\s+(--check|-c|--list-different|-l))\b`),
	"make":     anything,
	"cargo":    regexp.MustCompile(`^(build|test|check|clippy|run|bench|doc|fmt\s+--check)\b`),
	"python":   regexp.MustCompile(`^-m\s+(pytest|unittest|mypy|pyflakes|flake8|py_compile|compileall)\b`),
	"python3":  regexp.MustCompile(`^-m\s+(pytest|unittest|mypy|pyflakes|flake8|py_compile|compileall)\b`),
	"composer": regexp.MustCompile(`^(test|run-script|run|check-platform-reqs)\b`),
	"tsc":      anything,
	"pytest":   anything,
	"mypy":     anything,
	"phpunit":  anything,
	"phpstan":  anything,
	"eslint":   anything,
	"ruff":     regexp.MustCompile(`^check\b`),
	"flake8":   anything,
	"vitest":   anything,
	"jest":     anything,
}

// Targets and options a project command must not have.
var risky = regexp.MustCompile(`\b(install|uninstall|deploy|publish|release|push|clean|distclean|service)\b|(^|\s)--(fix|write)\b`)

// Interpreters that may run a script of the project (`node scripts/x.mjs`, `php bin/phpunit`).
var interpreters = setOf("node", "python", "python3", "php", "sh", "bash")

func setOf(list ...string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}

// resolvePath is the absolute path of p from the directory dir; "..", "." and duplicate
// slashes resolved.
func resolvePath(dir, p string) string {
	base := dir
	if strings.HasPrefix(p, "/") {
		base = ""
	}
	var out []string
	for _, seg := range strings.Split(base+"/"+p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	return "/" + strings.Join(out, "/")
}

func inside(root, p string) bool {
	r := root
	if !strings.HasSuffix(r, "/") {
		r += "/"
	}
	return p == root || strings.HasPrefix(p, r)
}

// Scratch is the folder the assistant uses freely in every mode, besides the project.
// WEBIDE_SCRATCH replaces it for the tests, whose projects are in /tmp.
var Scratch = "/tmp"

func init() {
	if d := os.Getenv("WEBIDE_SCRATCH"); d != "" {
		Scratch = d
	}
}

// InZone tells whether the absolute path p is in one of the folders dirs.
func InZone(p string, dirs ...string) bool {
	for _, d := range dirs {
		if inside(d, p) {
			return true
		}
	}
	return false
}

// pathOf is the absolute path of the word w from dir; "/~" (out of every folder) when it
// depends on the home or a variable.
func pathOf(dir, w string) string {
	if strings.HasPrefix(w, "~") || strings.Contains(w, "$") {
		return "/~"
	}
	return resolvePath(dir, w)
}

const ops = "|;&<>\n"

// redirMark starts the word that replaces an output redirection: the mark, then the target.
const redirMark = "\ue010"

var (
	substitution = regexp.MustCompile("\\$\\(([^()`]*)\\)|`([^`]*)`")
	quoted       = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
	streamRedir  = regexp.MustCompile(`\d?>&\d`)
	redirect     = regexp.MustCompile(`(?:\d|&)?>>?\|?\s*([^\s|;&<>]*)`)
	separators   = regexp.MustCompile(`&&|\|\||;|\||&|\n`)
	edges        = regexp.MustCompile(`^[\s(]+|[\s)]+$`)
	quotedWord   = regexp.MustCompile(`^(['"])(.*)(['"])$`)
	assignment   = regexp.MustCompile(`^\w+=`)
	flagWithArg  = regexp.MustCompile(`^-[nIPLdsEk]$`)
)

// mask hides the operators inside quotes (private use characters), unmask puts them back.
func mask(q string) string {
	return strings.Map(func(r rune) rune {
		if i := strings.IndexRune(ops, r); i >= 0 {
			return rune(0xe000 + i)
		}
		return r
	}, q)
}

func unmask(w string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0xe000 && r <= 0xe005 {
			return rune(ops[r-0xe000])
		}
		return r
	}, w)
}

// walk calls each for the simple commands of a shell command (wrappers removed), with the
// folder each one runs in (cwd, then the cd) and the targets of its output redirections. The
// substitutions are walked first, from cwd. False when it cannot follow the command or when
// each returns false.
func walk(command, cwd string, each func(words []string, dir string, redirs []string) bool) bool {
	c := strings.ReplaceAll(command, "\\\n", " ")
	// Substitutions: walked, then replaced by a plain word.
	for guard := 0; strings.Contains(c, "$(") || strings.Contains(c, "`"); guard++ {
		m := substitution.FindStringSubmatchIndex(c)
		if m == nil || guard > 20 {
			return false
		}
		inner := ""
		if m[2] >= 0 {
			inner = c[m[2]:m[3]]
		} else {
			inner = c[m[4]:m[5]]
		}
		if !walk(inner, cwd, each) {
			return false
		}
		c = c[:m[0]] + "x" + c[m[1]:]
	}
	masked := quoted.ReplaceAllStringFunc(c, mask)
	masked = redirect.ReplaceAllString(streamRedir.ReplaceAllString(masked, ""), " "+redirMark+"$1")
	dir := cwd
	for _, seg := range separators.Split(masked, -1) {
		var words, redirs []string
		for _, w := range strings.Fields(edges.ReplaceAllString(seg, "")) {
			w = unmask(w)
			target, isRedir := strings.CutPrefix(w, redirMark)
			if isRedir {
				w = target
			}
			if m := quotedWord.FindStringSubmatch(w); m != nil && m[1] == m[3] {
				w = m[2]
			}
			if !isRedir {
				words = append(words, w)
			} else if w == "" {
				return false
			} else {
				redirs = append(redirs, pathOf(dir, w))
			}
		}
		if len(words) > 0 && (words[0] == "cd" || words[0] == "pushd") {
			if len(words) > 1 {
				dir = pathOf(dir, words[1])
			} else {
				dir = "/~"
			}
			continue
		}
		if len(words) > 0 && words[0] == "popd" {
			dir = "/~" // unknown: out of the project from now on
			continue
		}
		if (len(words) > 0 || len(redirs) > 0) && !each(unwrap(words), dir, redirs) {
			return false
		}
	}
	return true
}

// RunsFreely tells whether the Plan mode runs the command without asking. root: the
// project; cwd: where the command starts (the project when empty).
func RunsFreely(command, root, cwd string) bool {
	if cwd == "" {
		cwd = root
	}
	return walk(command, cwd, func(words []string, dir string, redirs []string) bool {
		// Output redirections to /dev/null or to the scratch folder only.
		for _, t := range redirs {
			if !InZone(t, "/dev/null", Scratch) {
				return false
			}
		}
		return simpleRunsFreely(words, inside(root, dir), dir, root)
	})
}

// Commands that act beyond the files given to them: privileges, other machines, services,
// processes.
var escapes = setOf("sudo", "su", "doas", "pkexec", "ssh", "scp", "sftp", "systemctl", "service", "shutdown", "reboot", "mount", "umount", "chroot", "crontab", "kill", "pkill", "killall")

// StaysIn tells whether a command touches nothing out of the folders dirs, so that it runs
// without asking: reading commands run from anywhere on anything; the others run in the
// folders, write their output in them (or /dev/null), and their path arguments resolve in
// them. A guess like RunsFreely: a path made of a variable or of the home asks.
func StaysIn(command, cwd string, dirs ...string) bool {
	return walk(command, cwd, func(words []string, dir string, redirs []string) bool {
		for _, t := range redirs {
			if t != "/dev/null" && !InZone(t, dirs...) {
				return false
			}
		}
		if len(words) == 0 || readOnly(words) {
			return true
		}
		if !InZone(dir, dirs...) || escapes[words[0]] {
			return false
		}
		for _, w := range words {
			if strings.HasPrefix(w, "-") {
				_, w, _ = strings.Cut(w, "=") // --out=path
			}
			w = strings.Trim(w, `'"`)
			if (strings.Contains(w, "/") || strings.HasPrefix(w, "~") || strings.Contains(w, "$")) && !InZone(pathOf(dir, w), dirs...) {
				return false
			}
		}
		return true
	})
}

// unwrap removes assignments and wrappers: `LANG=C timeout 60 nice -n 5 xargs -0 grep x` → `grep x`;
// a known program called by its path in a bin folder becomes its name: `~/sdk/go/bin/go test` → `go test`.
func unwrap(words []string) []string {
	w := append([]string{}, words...)
	for {
		for len(w) > 0 && assignment.MatchString(w[0]) {
			w = w[1:]
		}
		if len(w) == 0 {
			return w
		}
		if name := binProgram(w[0]); name != "" {
			w[0] = name
		}
		head := w[0]
		if wrappers[head] {
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				flag := w[0]
				w = w[1:]
				if flagWithArg.MatchString(flag) && len(w) > 0 {
					w = w[1:]
				}
			}
			if head == "timeout" && len(w) > 0 && w[0] != "" && w[0][0] >= '0' && w[0][0] <= '9' {
				w = w[1:]
			}
			continue
		}
		return w
	}
}

var wrappers = setOf("time", "command", "nice", "timeout", "xargs", "env")

// binProgram is the name of a known program called by an absolute or home path in a bin
// folder (`/usr/bin/env`, `~/sdk/go/bin/go`), else "". A variable stays unknown.
func binProgram(word string) string {
	if !strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "~/") || strings.Contains(word, "$") {
		return ""
	}
	dir, name := word[:strings.LastIndex(word, "/")], word[strings.LastIndex(word, "/")+1:]
	if !strings.HasSuffix(dir, "/bin") && !strings.HasSuffix(dir, "/sbin") {
		return ""
	}
	if readCommands[name] || projectSub[name] != nil || escapes[name] || wrappers[name] || interpreters[name] {
		return name
	}
	return ""
}

// readOnly tells whether a simple command only reads.
func readOnly(words []string) bool {
	cmd, rest := words[0], words[1:]
	if cmd == "git" {
		// Global options: git --no-pager -C dir -c k=v log
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			opt := rest[0]
			rest = rest[1:]
			if (opt == "-C" || opt == "-c") && len(rest) > 0 {
				rest = rest[1:]
			}
		}
	}
	return readCommands[cmd] && readOnlyArgs(cmd, strings.Join(rest, " "))
}

func simpleRunsFreely(words []string, inProject bool, dir, root string) bool {
	if len(words) == 0 {
		return true
	}
	if readOnly(words) {
		return true
	}
	cmd, rest := words[0], words[1:]
	args := strings.Join(rest, " ")
	if !inProject || risky.MatchString(args) {
		return false
	}
	if sub := projectSub[cmd]; sub != nil && sub.MatchString(args) {
		return true
	}
	// A script of the project, run directly (a path) or by an interpreter (no -c / -e).
	if interpreters[cmd] {
		return len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && inside(root, resolvePath(dir, rest[0]))
	}
	return strings.Contains(cmd, "/") && inside(root, resolvePath(dir, cmd))
}

var (
	sedInPlace = regexp.MustCompile(`(^|\s)-i`)
	findExec   = regexp.MustCompile(`-(delete|exec|execdir|ok|fprint)`)
	fdExec     = regexp.MustCompile(`(^|\s)(-x|-X|--exec|--exec-batch)\b`)
	sortOutput = regexp.MustCompile(`(^|\s)(-o|--output)`)
	awkWrites  = regexp.MustCompile(`system\s*\(|>\s*"|\|\s*"`)
)

func readOnlyArgs(cmd, args string) bool {
	switch {
	case cmd == "sed" && sedInPlace.MatchString(args),
		cmd == "find" && findExec.MatchString(args),
		cmd == "fd" && fdExec.MatchString(args),
		cmd == "sort" && sortOutput.MatchString(args),
		cmd == "awk" && awkWrites.MatchString(args):
		return false
	}
	sub := readSub[cmd]
	return sub == nil || sub.MatchString(args)
}
