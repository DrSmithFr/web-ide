// Shell commands the Plan mode runs without asking: commands that only read (anywhere), and
// the build, test and lint commands or scripts of the project run from inside it. Anything
// else (installs, deletions, writes out of the project, unknown programs) waits for the user.
// A guess, kept conservative: what it cannot parse asks.

// Commands that only read, whatever their arguments (checked below for the risky ones).
const readCommands = new Set([
  'ls', 'cat', 'head', 'tail', 'less', 'more', 'grep', 'egrep', 'fgrep', 'rg', 'ag', 'fd', 'wc', 'file', 'stat', 'pwd', 'echo', 'printf', 'tree', 'du', 'df',
  'which', 'whereis', 'type', 'env', 'printenv', 'sort', 'uniq', 'cut', 'tr', 'diff', 'cmp', 'comm', 'jq', 'yq', 'basename', 'dirname', 'realpath', 'readlink',
  'date', 'whoami', 'id', 'uname', 'hostname', 'ps', 'nl', 'column', 'md5sum', 'sha1sum', 'sha256sum', 'true', 'false', 'test', '[', 'awk', 'sed', 'find',
  'xxd', 'hexdump', 'od', 'strings', 'tac', 'rev', 'paste', 'join', 'fold', 'seq', 'sleep', 'free', 'uptime', 'nproc', 'export',
  'go', 'gofmt', 'git', 'npm', 'node', 'python', 'python3', 'php', 'composer', 'cargo', 'make', 'docker',
])
const readSub: Record<string, RegExp> = {
  git: /^(status|log|diff|show|blame|ls-files|ls-tree|grep|rev-parse|rev-list|describe|shortlog|reflog|cat-file|merge-base|name-rev|for-each-ref|check-ignore|count-objects|whatchanged|range-diff|config\s+(--get|--list|-l)|remote(\s+-v)?$|branch(\s+(-a|-r|-v|-vv|--list|--show-current|--contains\s+\S+))*$|tag(\s+(-l|--list))?$|stash\s+(list|show)|worktree\s+list)/,
  go: /^(vet|list|doc|version|env)\b/,
  gofmt: /^(-l|-d)\b/,
  npm: /^(ls|list|view|outdated|explain|why|-v|--version)\b/,
  node: /^(-v|--version)$/,
  python: /^(-V|--version)$/,
  python3: /^(-V|--version)$/,
  php: /^(-v|--version|-l)\b/,
  composer: /^(show|outdated|licenses|validate|-V|--version)\b/,
  cargo: /^(tree|metadata|--version)\b/,
  make: /^(-n|--dry-run)\b/,
  docker: /^(ps|images|logs|inspect|version|info|compose\s+(ps|logs|config|ls|images))\b/,
}

// Build, test and lint commands: they may write build outputs in the project, never install,
// publish, deploy or rewrite sources. Run freely from inside the project only.
const projectSub: Record<string, RegExp> = {
  go: /^(build|test|vet|run|list|doc|version|env|mod\s+(graph|why|verify))\b/,
  npm: /^(test|t|run|run-script)\b/,
  pnpm: /^(test|t|run|lint|build|check|typecheck|why)\b/,
  yarn: /^(test|run|lint|build|check|typecheck|why)\b/,
  npx: /^(--no-install\s+)?(tsc|vitest|jest|mocha|eslint|playwright\s+test|prettier\s+(--check|-c|--list-different|-l))\b/,
  make: /^/,
  cargo: /^(build|test|check|clippy|run|bench|doc|fmt\s+--check)\b/,
  python: /^-m\s+(pytest|unittest|mypy|pyflakes|flake8|py_compile|compileall)\b/,
  python3: /^-m\s+(pytest|unittest|mypy|pyflakes|flake8|py_compile|compileall)\b/,
  composer: /^(test|run-script|run|check-platform-reqs)\b/,
  tsc: /^/,
  pytest: /^/,
  mypy: /^/,
  phpunit: /^/,
  phpstan: /^/,
  eslint: /^/,
  ruff: /^check\b/,
  flake8: /^/,
  vitest: /^/,
  jest: /^/,
}
// Targets and options a project command must not have.
const risky = /\b(install|uninstall|deploy|publish|release|push|clean|distclean|service)\b|(^|\s)--(fix|write)\b/
// Interpreters that may run a script of the project (`node scripts/x.mjs`, `php bin/phpunit`).
const interpreters = new Set(['node', 'python', 'python3', 'php', 'sh', 'bash'])

/** Absolute path of p from the directory dir; "..", "." and duplicate slashes resolved. */
function resolve(dir: string, p: string): string {
  const out: string[] = []
  for (const seg of `${p.startsWith('/') ? '' : dir}/${p}`.split('/')) {
    if (!seg || seg === '.') continue
    if (seg === '..') out.pop()
    else out.push(seg)
  }
  return '/' + out.join('/')
}

const ops = '|;&<>\n'

const inside = (root: string, p: string) => p === root || p.startsWith(root.endsWith('/') ? root : root + '/')

/**
 * Whether the Plan mode runs the command without asking. root: the project; cwd: where the
 * command starts (the project by default).
 */
export function runsFreely(command: string, root: string, cwd = root): boolean {
  let c = command.replace(/\\\n/g, ' ')
  // Substitutions: allowed when what they run is allowed, then replaced by a plain word.
  for (let guard = 0; /\$\(|`/.test(c); guard++) {
    const m = /\$\(([^()`]*)\)|`([^`]*)`/.exec(c)
    if (!m || guard > 20) return false
    if (!runsFreely(m[1] ?? m[2], root, cwd)) return false
    c = c.slice(0, m.index) + 'x' + c.slice(m.index + m[0].length)
  }
  // Quoted text is an argument: the operators inside do not count (hidden, then put back).
  const masked = c.replace(/'[^']*'|"(?:[^"\\]|\\.)*"/g, (q) => q.replace(/[|;&<>\n]/g, (ch) => String.fromCharCode(0xe000 + ops.indexOf(ch))))
  // Output redirections other than to /dev/null, to /tmp or between streams.
  const noSafe = masked.replace(/\d?>&\d/g, '').replace(/(&|\d)?>>?\s*(\/dev\/null|\/tmp\/[\w./-]+)/g, '')
  if (/>/.test(noSafe)) return false
  let dir = cwd
  for (const seg of noSafe.split(/&&|\|\||;|\||&|\n/)) {
    const words = seg
      .replace(/^[\s(]+|[\s)]+$/g, '')
      .split(/\s+/)
      .filter(Boolean)
      .map((w) => w.replace(/[\ue000-\ue005]/g, (ch) => ops[ch.charCodeAt(0) - 0xe000]).replace(/^(['"])(.*)\1$/, '$2'))
    if (!words.length) continue
    if (words[0] === 'cd' || words[0] === 'pushd') {
      dir = words[1] && !words[1].startsWith('~') ? resolve(dir, words[1]) : '/~'
      continue
    }
    if (words[0] === 'popd') {
      dir = '/~' // unknown: out of the project from now on
      continue
    }
    if (!simpleRunsFreely(unwrap(words), inside(root, dir), dir, root)) return false
  }
  return true
}

/** Removes assignments and wrappers: `LANG=C timeout 60 nice -n 5 xargs -0 grep x` → `grep x`. */
function unwrap(words: string[]): string[] {
  const w = [...words]
  for (;;) {
    while (w.length && /^\w+=/.test(w[0])) w.shift()
    const head = w[0]
    if (head === 'time' || head === 'command' || head === 'nice' || head === 'timeout' || head === 'xargs' || head === 'env') {
      w.shift()
      while (w.length && w[0].startsWith('-')) {
        const flag = w.shift()!
        if (/^-[nIPLdsEk]$/.test(flag)) w.shift()
      }
      if (head === 'timeout' && w.length && /^\d/.test(w[0])) w.shift()
      continue
    }
    return w
  }
}

function simpleRunsFreely(words: string[], inProject: boolean, dir: string, root: string): boolean {
  if (!words.length) return true
  const [cmd, ...rest] = words
  if (cmd === 'git') {
    // Global options: git --no-pager -C dir -c k=v log
    while (rest.length && rest[0].startsWith('-')) {
      const opt = rest.shift()!
      if (opt === '-C' || opt === '-c') rest.shift()
    }
  }
  const args = rest.join(' ')
  if (readCommands.has(cmd) && readOnlyArgs(cmd, args)) return true
  if (!inProject) return false
  if (risky.test(args)) return false
  const sub = projectSub[cmd]
  if (sub && sub.test(args)) return true
  // A script of the project, run directly (a path) or by an interpreter (no -c / -e).
  if (interpreters.has(cmd)) return !!rest[0] && !rest[0].startsWith('-') && inside(root, resolve(dir, rest[0]))
  return cmd.includes('/') && inside(root, resolve(dir, cmd))
}

function readOnlyArgs(cmd: string, args: string): boolean {
  if (cmd === 'sed' && /(^|\s)-i/.test(args)) return false
  if (cmd === 'find' && /-(delete|exec|execdir|ok|fprint)/.test(args)) return false
  if (cmd === 'fd' && /(^|\s)(-x|-X|--exec|--exec-batch)\b/.test(args)) return false
  if (cmd === 'sort' && /(^|\s)(-o|--output)/.test(args)) return false
  if (cmd === 'awk' && /system\s*\(|>\s*"|\|\s*"/.test(args)) return false
  const sub = readSub[cmd]
  return !sub || sub.test(args)
}
