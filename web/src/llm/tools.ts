// Tools the model can call: read and change the project files, search, and ask the
// language servers. They run in the page with the same RPCs as the rest of the IDE.
import { on as onPod, request } from '../pod/rpc'
import { activeTab, applyRemote, diagnostics, fileUri, flushLsp, getDoc, loadDoc, mutate, openFile, relPath, root, session } from '../state/project'
import { consoles, setConsoleList } from '../console/consoles'
import { refreshGit } from '../state/git'
import { lspLanguage, lspLanguageId } from '../editor/languages'
import { lineHunks } from '../editor/linediff'
import { flatten, symbolKinds, toLocations, type DocumentSymbol, type Location } from '../lsp/client'
import type { DiffLine, ToolCall } from './state'
import { askUserDef, kanbanReadDefs, kanbanToolNames, kanbanWriteDefs, runKanbanTool } from './kanbanTools'

export interface ToolResult {
  /** Text given back to the model. */
  content: string
  /** One line shown folded in the conversation. */
  summary: string
  status: 'ok' | 'error' | 'denied'
  diff?: DiffLine[]
}

/** Asks the user before a change; resolves false when refused. */
export type Confirm = (req: { call: ToolCall; kind: 'edit' | 'command'; path?: string; diff?: DiffLine[]; created?: boolean; command?: string }) => Promise<boolean>

const str = (description: string) => ({ type: 'string', description })
const int = (description: string) => ({ type: 'integer', description })
const fn = (name: string, description: string, properties: Record<string, any>, required: string[] = []) => ({
  type: 'function',
  function: { name, description, parameters: { type: 'object', properties, required } },
})

const pathArg = str('Chemin relatif à la racine du projet (ou absolu)')
const lineArg = int('Numéro de ligne (commence à 1), comme affiché par read_file')
const symbolArg = str('Le symbole tel qu’il est écrit sur cette ligne (nom de fonction, variable, type…)')

export const toolDefs = [
  fn('list_dir', 'Liste le contenu d’un dossier du projet.', { path: str('Dossier, relatif à la racine ("" ou "." pour la racine)') }),
  fn('find_files', 'Cherche des fichiers du projet dont le chemin contient le motif (ou correspond au glob * ?).', { pattern: str('Ex. "handler", "*.go", "src/**/*.tsx"') }, ['pattern']),
  fn(
    'read_file',
    'Lit un fichier du projet. Chaque ligne est préfixée par son numéro et une tabulation (le préfixe ne fait pas partie du fichier).',
    { path: pathArg, start_line: int('Première ligne (1 par défaut)'), end_line: int('Dernière ligne incluse') },
    ['path'],
  ),
  fn(
    'search_text',
    'Recherche un texte (ou une expression régulière RE2) dans tous les fichiers du projet.',
    { query: str('Texte ou expression'), regex: { type: 'boolean', description: 'query est une expression régulière' }, include: str('Globs des noms de fichier, séparés par des virgules, ex. "*.go,*.ts"') },
    ['query'],
  ),
  fn(
    'edit_file',
    'Remplace un passage exact d’un fichier. old_string doit apparaître une seule fois (sans les numéros de ligne de read_file) : inclure assez de contexte. Pour créer un fichier, utiliser write_file.',
    { path: pathArg, old_string: str('Texte exact à remplacer'), new_string: str('Texte de remplacement') },
    ['path', 'old_string', 'new_string'],
  ),
  fn('write_file', 'Crée un fichier ou remplace tout son contenu.', { path: pathArg, content: str('Contenu complet du fichier') }, ['path', 'content']),
  fn('lsp_symbols', 'Structure d’un fichier (classes, fonctions, méthodes…) par le serveur de langage.', { path: pathArg }, ['path']),
  fn('lsp_workspace_symbols', 'Cherche un symbole par son nom dans tout le projet (serveur de langage).', { query: str('Nom ou début de nom'), path: str('Un fichier du langage visé (facultatif : fichier actif par défaut)') }, ['query']),
  fn('lsp_definition', 'Trouve la définition du symbole écrit à cette ligne.', { path: pathArg, line: lineArg, symbol: symbolArg }, ['path', 'line', 'symbol']),
  fn('lsp_references', 'Trouve les utilisations du symbole écrit à cette ligne.', { path: pathArg, line: lineArg, symbol: symbolArg }, ['path', 'line', 'symbol']),
  fn('lsp_hover', 'Type et documentation du symbole écrit à cette ligne.', { path: pathArg, line: lineArg, symbol: symbolArg }, ['path', 'line', 'symbol']),
  fn('lsp_diagnostics', 'Erreurs et avertissements du serveur de langage, pour un fichier ou pour les fichiers ouverts.', { path: str('Fichier (facultatif)') }),
  fn('load_skill', 'Charge les instructions complètes d’un skill de la liste (et la liste de ses autres fichiers).', { name: str('Nom du skill') }, ['name']),
  fn('read_skill_file', 'Lit un autre fichier d’un skill (chemin relatif à son dossier, tel que listé par load_skill).', { name: str('Nom du skill'), file: str('Chemin du fichier dans le skill') }, ['name', 'file']),
  fn(
    'open_file',
    'Ouvre un fichier dans l’éditeur de l’utilisateur pour le lui montrer, en sélectionnant éventuellement des lignes.',
    { path: pathArg, line: int('Première ligne à montrer (commence à 1)'), end_line: int('Dernière ligne à sélectionner') },
    ['path'],
  ),
  fn(
    'focus',
    'Met au premier plan un élément de l’IDE : un fichier déjà ouvert, un panneau (explorer, search, git, connections, database, structure, conflicts, extensions, properties), une console ou la liste des problèmes.',
    {
      target: { type: 'string', enum: ['file', 'panel', 'console', 'problems'], description: 'Type d’élément' },
      path: str('Fichier (target=file)'),
      panel: str('Panneau (target=panel)'),
      console_id: str('Identifiant de console (target=console), voir list_consoles'),
    },
    ['target'],
  ),
  fn(
    'bash',
    'Exécute une commande shell (sh -c, dans le projet) et renvoie sa sortie (stdout et stderr) et son code de sortie. À utiliser pour toutes tes opérations : tests, compilation, git, outils en ligne de commande. Pas de terminal ni d’entrée : pas de commande interactive.',
    { command: str('Commande, ex. "go test ./..."'), cwd: str('Dossier de travail (racine du projet par défaut)'), timeout: int('Délai en secondes (120 par défaut, 1800 max)') },
    ['command'],
  ),
  fn(
    'run_command',
    'Lance une commande dans une nouvelle console de l’IDE, visible par l’utilisateur (serveur de développement, watcher, commande qu’il veut suivre ou utiliser). Attend sa fin ou le délai, puis renvoie le début de sa sortie ; elle continue de tourner ensuite. Pour tes propres opérations, utilise bash.',
    { command: str('Commande, ex. "npm run dev"'), cwd: str('Dossier de travail (racine du projet par défaut)'), timeout: int('Secondes à attendre avant de rendre la main (20 par défaut, 600 max)') },
    ['command'],
  ),
  fn('list_consoles', 'Liste les consoles ouvertes (terminaux et commandes) avec leur état.', {}),
  fn('read_console', 'Lit la fin de la sortie d’une console.', { console_id: str('Identifiant de console'), lines: int('Nombre de lignes (200 par défaut)') }, ['console_id']),
  fn(
    'console_input',
    'Tape du texte dans une console (terminal interactif, programme qui attend une réponse). Un retour à la ligne est ajouté sauf si enter=false.',
    { console_id: str('Identifiant de console'), text: str('Texte à taper'), enter: { type: 'boolean', description: 'Valider avec Entrée (oui par défaut)' } },
    ['console_id', 'text'],
  ),
]

export const writeTools = new Set(['edit_file', 'write_file'])

/** Tools handled by the agent loop itself. */
export const agentToolDefs = {
  exitPlan: fn(
    'exit_plan_mode',
    'Présente le plan terminé à l’utilisateur (mode Plan). Il peut l’accepter, ce qui passe en mode Build pour l’exécuter, ou demander des changements. Après cet appel, attends sa réponse.',
    { plan: str('Le plan complet en Markdown : objectif, fichiers, étapes numérotées, tests') },
    ['plan'],
  ),
  compact: fn(
    'compact_conversation',
    'Résume les anciens messages de la conversation pour libérer du contexte (le dernier échange est gardé). À utiliser quand une tâche est terminée ou que la conversation devient longue.',
    { instructions: str('Ce que le résumé doit garder en priorité (facultatif)') },
  ),
}

/**
 * Tools offered to the model in a mode: no file change in Plan, exit_plan_mode only there.
 * The kanban tools that change a ticket come with a conversation linked to a ticket.
 */
export function toolsFor(mode: 'plan' | 'build', ticket?: { id: number; role: string }) {
  const base = mode === 'plan' ? toolDefs.filter((t) => !writeTools.has(t.function.name)) : toolDefs
  const kanban = [...kanbanReadDefs, ...(ticket ? kanbanWriteDefs : []), askUserDef]
  // The briefing and the plan of a ticket end in the ticket itself, not in exit_plan_mode.
  const exit = mode === 'plan' && ticket?.role !== 'briefing' && ticket?.role !== 'plan' ? [agentToolDefs.exitPlan] : []
  return [...base, ...kanban, ...exit, agentToolDefs.compact]
}

// Commands that only read (Plan mode runs them without asking).
const readCommands = new Set([
  'ls', 'cat', 'head', 'tail', 'less', 'more', 'grep', 'egrep', 'fgrep', 'rg', 'ag', 'wc', 'file', 'stat', 'pwd', 'echo', 'printf', 'tree', 'du', 'df',
  'which', 'whereis', 'type', 'env', 'printenv', 'sort', 'uniq', 'cut', 'tr', 'diff', 'cmp', 'jq', 'yq', 'basename', 'dirname', 'realpath', 'readlink',
  'date', 'whoami', 'id', 'uname', 'hostname', 'ps', 'nl', 'column', 'md5sum', 'sha256sum', 'true', 'false', 'test', '[', 'awk', 'sed', 'find', 'go', 'git',
  'npm', 'node', 'python', 'python3', 'php', 'composer', 'cargo', 'make', 'docker',
])
const readSub: Record<string, RegExp> = {
  git: /^(status|log|diff|show|blame|ls-files|ls-tree|grep|rev-parse|describe|shortlog|reflog|cat-file|config\s+--get|remote(\s+-v)?$|branch(\s+(-a|-r|-v|-vv|--list|--show-current))*$|tag(\s+(-l|--list))?$|stash\s+list)/,
  go: /^(vet|list|doc|version|env)\b/,
  npm: /^(ls|list|view|outdated|explain|why|-v|--version)\b/,
  node: /^(-v|--version)$/,
  python: /^(-V|--version)$/,
  python3: /^(-V|--version)$/,
  php: /^(-v|--version|-l)\b/,
  composer: /^(show|outdated|licenses|-V|--version)\b/,
  cargo: /^(tree|metadata|--version)\b/,
  make: /^(-n|--dry-run)\b/,
  docker: /^(ps|images|logs|inspect|version|info)\b/,
}

/**
 * Guesses whether a shell command only reads. Conservative: redirections to files,
 * substitutions and unknown commands count as changes.
 */
export function isReadOnlyCommand(command: string): boolean {
  const c = command.replace(/\\\n/g, ' ')
  if (/`|\$\(/.test(c)) return false
  // Output redirections other than to /dev/null or between streams.
  const noSafe = c.replace(/\d?>&\d/g, '').replace(/\d?>>?\s*\/dev\/null/g, '')
  if (/>/.test(noSafe)) return false
  for (const seg of c.split(/&&|\|\||;|\||\n/)) {
    const words = seg.trim().split(/\s+/).filter(Boolean)
    // Leading variable assignments (LANG=C cmd).
    while (words.length && /^\w+=/.test(words[0])) words.shift()
    if (!words.length) continue
    const [cmd, ...rest] = words
    if (!readCommands.has(cmd)) return false
    const args = rest.join(' ')
    if (cmd === 'sed' && /(^|\s)-i/.test(args)) return false
    if (cmd === 'find' && /-(delete|exec|execdir|ok|fprint)/.test(args)) return false
    if (cmd === 'sort' && /(^|\s)(-o|--output)/.test(args)) return false
    if (cmd === 'awk' && /system\s*\(|>\s*"/.test(args)) return false
    const sub = readSub[cmd]
    if (sub && !sub.test(args)) return false
  }
  return true
}

const MAX_LINES = 1500
const MAX_CHARS = 120_000

/** Absolute path in the project; "..", "." and duplicate slashes are resolved. */
export function absPath(p: string): string {
  p = (p ?? '').trim()
  const base = p.startsWith('/') ? '' : root()
  const out: string[] = []
  for (const seg of `${base}/${p}`.split('/')) {
    if (!seg || seg === '.') continue
    if (seg === '..') out.pop()
    else out.push(seg)
  }
  return '/' + out.join('/')
}

function parseArgs(call: ToolCall): Record<string, any> {
  try {
    const v = JSON.parse(call.function.arguments || '{}')
    return v && typeof v === 'object' ? v : {}
  } catch {
    throw new Error(`arguments JSON invalides : ${call.function.arguments.slice(0, 200)}`)
  }
}

export async function runTool(call: ToolCall, confirm: Confirm, signal?: AbortSignal, mode: 'plan' | 'build' = 'build', ticket?: number): Promise<ToolResult> {
  const name = call.function.name
  try {
    const a = parseArgs(call)
    if (kanbanToolNames.has(name)) return await runKanbanTool(name, a, ticket)
    if (mode === 'plan') {
      if (writeTools.has(name)) return fail('mode Plan : les fichiers ne peuvent pas être modifiés. Présente le plan avec exit_plan_mode ; il sera exécuté en mode Build.')
      // A command that may change something waits for the user.
      if ((name === 'bash' || name === 'run_command') && !isReadOnlyCommand(String(a.command ?? ''))) {
        if (!(await confirm({ call, kind: 'command', command: String(a.command ?? '') })))
          return { content: 'L’utilisateur a refusé cette commande (mode Plan : seules les lectures sont libres).', summary: 'commande refusée', status: 'denied' }
      }
    }
    switch (name) {
      case 'list_dir':
        return await listDir(a.path ?? '')
      case 'find_files':
        return await findFiles(String(a.pattern ?? ''))
      case 'read_file':
        return await readFile(a.path, a.start_line, a.end_line)
      case 'search_text':
        return await searchText(String(a.query ?? ''), !!a.regex, a.include ?? '')
      case 'edit_file':
        return await editFile(call, a.path, a.old_string, a.new_string, confirm)
      case 'write_file':
        return await writeFile(call, a.path, a.content, confirm)
      case 'lsp_symbols':
        return await lspSymbols(a.path)
      case 'lsp_workspace_symbols':
        return await lspWorkspaceSymbols(String(a.query ?? ''), a.path)
      case 'lsp_definition':
      case 'lsp_references':
      case 'lsp_hover':
        return await lspAt(name, a.path, Number(a.line), String(a.symbol ?? ''))
      case 'lsp_diagnostics':
        return await lspDiagnostics(a.path)
      case 'load_skill':
        return await loadSkill(String(a.name ?? ''))
      case 'read_skill_file':
        return await readSkillFile(String(a.name ?? ''), String(a.file ?? ''))
      case 'open_file':
        return await showFile(a.path, a.line, a.end_line)
      case 'focus':
        return await focus(a)
      case 'bash':
        return await bash(String(a.command ?? ''), a.cwd, a.timeout, signal)
      case 'run_command':
        return await runCommand(String(a.command ?? ''), a.cwd, a.timeout)
      case 'list_consoles':
        return listConsoles()
      case 'read_console':
        return await readConsole(String(a.console_id ?? ''), a.lines)
      case 'console_input':
        return await consoleInput(String(a.console_id ?? ''), String(a.text ?? ''), a.enter !== false)
    }
    return fail(`outil inconnu : ${name}`)
  } catch (e) {
    return fail((e as Error).message)
  }
}

function fail(message: string): ToolResult {
  return { content: `Erreur : ${message}`, summary: message, status: 'error' }
}

function ok(content: string, summary: string, diff?: DiffLine[]): ToolResult {
  return { content, summary, status: 'ok', diff }
}

function plural(n: number, word: string) {
  return `${n} ${word}${n > 1 ? 's' : ''}`
}

// ---------- files ----------

async function listDir(p: string): Promise<ToolResult> {
  const abs = absPath(p)
  const entries: { name: string; dir: boolean; size: number }[] = await request('fs.list', { path: abs })
  entries.sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name))
  const lines = entries.slice(0, 500).map((e) => (e.dir ? `${e.name}/` : `${e.name}  (${e.size} o)`))
  if (entries.length > 500) lines.push(`… ${entries.length - 500} autres entrées`)
  return ok(lines.join('\n') || '(dossier vide)', `${abs === root() ? '.' : relPath(abs)} : ${plural(entries.length, 'entrée')}`)
}

function globToRegExp(glob: string): RegExp {
  let re = ''
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === '*') {
      if (glob[i + 1] === '*') {
        re += '.*'
        i++
        if (glob[i + 1] === '/') i++
      } else re += '[^/]*'
    } else if (c === '?') re += '[^/]'
    else re += c.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  }
  return new RegExp(glob.includes('/') ? `^${re}$` : `(^|/)${re}$`, 'i')
}

async function findFiles(pattern: string): Promise<ToolResult> {
  const files: string[] = await request('search.files')
  let list: string[]
  if (/[*?]/.test(pattern)) {
    const re = globToRegExp(pattern)
    list = files.filter((f) => re.test(f))
  } else {
    const q = pattern.toLowerCase()
    list = files.filter((f) => f.toLowerCase().includes(q))
  }
  const shown = list.slice(0, 200)
  let text = shown.join('\n') || 'Aucun fichier trouvé.'
  if (list.length > shown.length) text += `\n… ${list.length - shown.length} autres (préciser le motif)`
  return ok(text, `« ${pattern} » : ${plural(list.length, 'fichier')}`)
}

async function readText(abs: string): Promise<string> {
  const f = await request('fs.read', { path: abs })
  if (f.binary) throw new Error(`${relPath(abs)} est un fichier binaire ou trop gros`)
  return f.content as string
}

async function readFile(p: string, start?: number, end?: number): Promise<ToolResult> {
  if (!p) throw new Error('path manquant')
  const abs = absPath(p)
  const text = await readText(abs)
  const lines = text.split('\n')
  const from = Math.max(1, Math.floor(start || 1))
  let to = Math.min(lines.length, Math.floor(end || lines.length))
  let out = ''
  let truncated = false
  for (let i = from; i <= to; i++) {
    const row = `${i}\t${lines[i - 1]}\n`
    if (i - from >= MAX_LINES || out.length + row.length > MAX_CHARS) {
      truncated = true
      to = i - 1
      break
    }
    out += row
  }
  if (truncated) out += `… (tronqué : lire la suite avec start_line=${to + 1})\n`
  if (from > lines.length) out = `(le fichier n’a que ${lines.length} lignes)`
  return ok(out, `${relPath(abs)} : lignes ${from}-${to} sur ${lines.length}`)
}

async function searchText(query: string, regex: boolean, include: string): Promise<ToolResult> {
  if (!query) throw new Error('query manquante')
  const r = await request('search.grep', { query, regex, caseSensitive: false, wholeWord: false, include, max: 200 })
  const lines = (r.matches as { path: string; line: number; text: string }[]).map((m) => `${relPath(m.path)}:${m.line}: ${m.text.trim().slice(0, 300)}`)
  let text = lines.join('\n') || 'Aucun résultat.'
  if (r.truncated) text += '\n… (résultats tronqués, préciser la recherche)'
  return ok(text, `« ${query} » : ${plural(r.matches.length, 'résultat')}${r.truncated ? '+' : ''}`)
}

/** Unified diff of two texts with 2 lines of context, at most 400 lines. */
export function diffLines(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split('\n')
  const b = newText.split('\n')
  const hunks = lineHunks(a, b)
  if (!hunks) return [{ t: '…', text: 'différence trop grande pour être affichée' }]
  const out: DiffLine[] = []
  const ctx = 2
  let lastA = -1
  for (const h of hunks) {
    const [as, al] = h.a
    const [bs, bl] = h.b
    const from = Math.max(as - ctx, lastA + 1, 0)
    if (lastA >= 0 && from > lastA + 1) out.push({ t: '…', text: '' })
    else if (lastA < 0 && from > 0) out.push({ t: '…', text: `ligne ${from + 1}` })
    for (let i = from; i < as; i++) out.push({ t: ' ', text: a[i] })
    for (let i = as; i < as + al; i++) out.push({ t: '-', text: a[i] })
    for (let i = bs; i < bs + bl; i++) out.push({ t: '+', text: b[i] })
    const end = Math.min(as + al + ctx, a.length)
    for (let i = as + al; i < end; i++) out.push({ t: ' ', text: a[i] })
    lastA = end - 1
    if (out.length > 400) {
      out.length = 400
      out.push({ t: '…', text: 'suite du diff masquée' })
      break
    }
  }
  return out
}

async function exists(abs: string) {
  try {
    await request('fs.stat', { path: abs })
    return true
  } catch {
    return false
  }
}

/** Writes the file, then updates the open editor (the pod does not echo our own writes). */
async function commit(abs: string, content: string) {
  const r = await request('fs.write', { path: abs, content })
  const doc = getDoc(abs)
  if (doc) applyRemote(doc, content, r.rev, true)
  refreshGit()
}

async function editFile(call: ToolCall, p: string, oldStr: string, newStr: string, confirm: Confirm): Promise<ToolResult> {
  if (!p) throw new Error('path manquant')
  if (typeof oldStr !== 'string' || typeof newStr !== 'string') throw new Error('old_string et new_string sont obligatoires')
  if (oldStr === newStr) throw new Error('old_string et new_string sont identiques')
  const abs = absPath(p)
  const text = await readText(abs)
  let at = text.indexOf(oldStr)
  let old = oldStr
  if (at < 0 && text.includes('\r\n')) {
    // The model usually writes LF; the file uses CRLF.
    old = oldStr.replace(/\r?\n/g, '\r\n')
    newStr = newStr.replace(/\r?\n/g, '\r\n')
    at = text.indexOf(old)
  }
  if (at < 0) throw new Error(`old_string introuvable dans ${relPath(abs)} (relire le fichier avec read_file et copier le texte exact, sans les numéros de ligne)`)
  if (text.indexOf(old, at + 1) >= 0) throw new Error(`old_string apparaît plusieurs fois dans ${relPath(abs)} : ajouter du contexte pour qu’il soit unique`)
  const next = text.slice(0, at) + newStr + text.slice(at + old.length)
  const diff = diffLines(text, next)
  if (!(await confirm({ call, kind: 'edit', path: abs, diff, created: false }))) return { content: 'L’utilisateur a refusé cette modification.', summary: 'modification refusée', status: 'denied', diff }
  await commit(abs, next)
  const line = text.slice(0, at).split('\n').length
  const added = diff.filter((d) => d.t === '+').length
  const removed = diff.filter((d) => d.t === '-').length
  return ok(`Modification appliquée à ${relPath(abs)} (ligne ${line}).`, `${relPath(abs)} : +${added} −${removed}`, diff)
}

async function writeFile(call: ToolCall, p: string, content: string, confirm: Confirm): Promise<ToolResult> {
  if (!p) throw new Error('path manquant')
  if (typeof content !== 'string') throw new Error('content manquant')
  const abs = absPath(p)
  const created = !(await exists(abs))
  const before = created ? '' : await readText(abs)
  if (!created && before === content) return ok(`${relPath(abs)} a déjà ce contenu.`, `${relPath(abs)} : inchangé`)
  const diff = diffLines(before, content)
  if (created && diff[0]?.t === '-') diff.shift() // the empty line of "nothing"
  if (!(await confirm({ call, kind: 'edit', path: abs, diff, created }))) return { content: 'L’utilisateur a refusé cette écriture.', summary: 'écriture refusée', status: 'denied', diff }
  await commit(abs, content)
  const n = content.split('\n').length
  return ok(`${relPath(abs)} ${created ? 'créé' : 'remplacé'} (${plural(n, 'ligne')}).`, `${relPath(abs)} ${created ? 'créé' : 'remplacé'} (${plural(n, 'ligne')})`, diff)
}

// ---------- language servers ----------

function langOf(abs: string) {
  const lang = lspLanguage(abs)
  if (!lang) throw new Error(`pas de serveur de langage pour ${relPath(abs)}`)
  return lang
}

/** Runs fn with the file known to the language server (opened for the request if needed). */
async function withLspDoc<T>(abs: string, fn: (text: string) => Promise<T>): Promise<T> {
  const lang = langOf(abs)
  const doc = getDoc(abs)
  if (doc && !doc.readOnly) {
    flushLsp(abs)
    return fn(doc.text)
  }
  const text = await readText(abs)
  const uri = fileUri(abs)
  await request('lsp.notify', { lang, method: 'textDocument/didOpen', params: { textDocument: { uri, languageId: lspLanguageId(abs), version: 1, text } } })
  try {
    return await fn(text)
  } finally {
    request('lsp.notify', { lang, method: 'textDocument/didClose', params: { textDocument: { uri } } }).catch(() => {})
  }
}

function lspRequest<T = any>(abs: string, method: string, params: any): Promise<T> {
  return request('lsp.request', { lang: langOf(abs), method, params })
}

async function lspSymbols(p: string): Promise<ToolResult> {
  if (!p) throw new Error('path manquant')
  const abs = absPath(p)
  const symbols = await withLspDoc(abs, () => lspRequest<DocumentSymbol[] | null>(abs, 'textDocument/documentSymbol', { textDocument: { uri: fileUri(abs) } }))
  const list = symbols ?? []
  // Old servers answer SymbolInformation (flat, with a location).
  const rows = (list[0] as any)?.location
    ? (list as any[]).map((s) => `${symbolKinds[s.kind]?.[0] ?? '?'} ${s.name}${s.containerName ? ` (dans ${s.containerName})` : ''} · ligne ${s.location.range.start.line + 1}`)
    : flatten(list).map(({ s, depth }) => `${'  '.repeat(depth)}${symbolKinds[s.kind]?.[0] ?? '?'} ${s.name}${s.detail ? ` ${s.detail}` : ''} · ligne ${s.selectionRange.start.line + 1}`)
  return ok(rows.join('\n') || 'Aucun symbole.', `${relPath(abs)} : ${plural(rows.length, 'symbole')}`)
}

async function lspWorkspaceSymbols(query: string, p?: string): Promise<ToolResult> {
  const ref = p ? absPath(p) : activeTab()?.path
  if (!ref) throw new Error('indiquer path : un fichier du langage visé')
  const res = await lspRequest<any[]>(ref, 'workspace/symbol', { query })
  const list = (res ?? []).slice(0, 100)
  const rows = list.map((s) => {
    const loc = s.location?.range ? `${relPath(decodeURIComponent(s.location.uri.replace(/^file:\/\//, '')))}:${s.location.range.start.line + 1}` : s.location?.uri ?? ''
    return `${symbolKinds[s.kind]?.[0] ?? '?'} ${s.name}${s.containerName ? ` (${s.containerName})` : ''} · ${loc}`
  })
  if ((res ?? []).length > list.length) rows.push(`… ${res.length - list.length} autres`)
  return ok(rows.join('\n') || 'Aucun symbole.', `« ${query} » : ${plural((res ?? []).length, 'symbole')}`)
}

/** Column of the symbol in the line (whole word first). */
function columnOf(lineText: string, symbol: string): number {
  if (!symbol) return -1
  const esc = symbol.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const m = new RegExp(`(?<![\\w$])${esc}(?![\\w$])`).exec(lineText)
  if (m) return m.index
  return lineText.indexOf(symbol)
}

async function locationLines(locs: Location[]): Promise<string[]> {
  const cache = new Map<string, string[]>()
  const out: string[] = []
  for (const l of locs) {
    if (!cache.has(l.path)) {
      try {
        cache.set(l.path, (await readText(l.path)).split('\n'))
      } catch {
        cache.set(l.path, [])
      }
    }
    const text = (cache.get(l.path)![l.range.start.line] ?? '').trim().slice(0, 200)
    out.push(`${relPath(l.path)}:${l.range.start.line + 1}:${l.range.start.character + 1}  ${text}`)
  }
  return out
}

async function lspAt(name: string, p: string, line: number, symbol: string): Promise<ToolResult> {
  if (!p) throw new Error('path manquant')
  if (!line || line < 1) throw new Error('line manquante (commence à 1)')
  const abs = absPath(p)
  return withLspDoc(abs, async (text) => {
    const lineText = text.split('\n')[line - 1]
    if (lineText === undefined) throw new Error(`${relPath(abs)} n’a pas de ligne ${line}`)
    const col = columnOf(lineText, symbol)
    if (col < 0) throw new Error(`« ${symbol} » absent de la ligne ${line} : ${lineText.trim().slice(0, 200)}`)
    const position = { textDocument: { uri: fileUri(abs) }, position: { line: line - 1, character: col } }
    if (name === 'lsp_hover') {
      const res = await lspRequest<any>(abs, 'textDocument/hover', position)
      const part = (c: any): string => (typeof c === 'string' ? c : c?.value ?? '')
      const hover = !res?.contents ? '' : Array.isArray(res.contents) ? res.contents.map(part).join('\n\n') : part(res.contents)
      return ok(hover.trim() || 'Aucune information.', `${symbol} : ${hover ? 'documentation' : 'rien'}`)
    }
    const method = name === 'lsp_definition' ? 'textDocument/definition' : 'textDocument/references'
    const params = name === 'lsp_definition' ? position : { ...position, context: { includeDeclaration: false } }
    const locs = toLocations(await lspRequest(abs, method, params))
    const shown = await locationLines(locs.slice(0, 100))
    if (locs.length > shown.length) shown.push(`… ${locs.length - shown.length} autres`)
    const what = name === 'lsp_definition' ? 'définition' : 'référence'
    return ok(shown.join('\n') || `Aucune ${what}.`, `${symbol} : ${plural(locs.length, what)}`)
  })
}

const severities = ['', 'erreur', 'avertissement', 'info', 'astuce']

function formatDiagnostics(path: string): string[] {
  return (diagnostics[path] ?? []).map((d) => `${relPath(path)}:${d.range.start.line + 1}:${d.range.start.character + 1} [${severities[d.severity ?? 1] ?? '?'}] ${d.message}${d.source ? ` (${d.source})` : ''}`)
}

async function lspDiagnostics(p?: string): Promise<ToolResult> {
  if (!p) {
    const rows = Object.keys(diagnostics).flatMap(formatDiagnostics)
    return ok(rows.slice(0, 300).join('\n') || 'Aucun diagnostic sur les fichiers ouverts.', plural(rows.length, 'diagnostic'))
  }
  const abs = absPath(p)
  let rows: string[]
  if (getDoc(abs)) rows = formatDiagnostics(abs)
  else {
    // Not open: open it in the server and read the diagnostics it publishes (before the
    // close, which clears them).
    const before = diagnostics[abs]
    rows = await withLspDoc(abs, async () => {
      const until = Date.now() + 4000
      while (Date.now() < until && diagnostics[abs] === before) await new Promise((r) => setTimeout(r, 150))
      return formatDiagnostics(abs)
    })
  }
  return ok(rows.join('\n') || `Aucun diagnostic pour ${relPath(abs)}.`, `${relPath(abs)} : ${plural(rows.length, 'diagnostic')}`)
}

// ---------- skills ----------

async function loadSkill(name: string): Promise<ToolResult> {
  if (!name) throw new Error('name manquant')
  const s = await request('llm.skill.read', { name })
  let text = s.content as string
  if (s.files?.length) text += `\n\n---\nAutres fichiers du skill (read_skill_file) :\n${(s.files as string[]).map((f) => `- ${f}`).join('\n')}`
  return ok(text, `skill ${name} chargé`)
}

async function readSkillFile(name: string, file: string): Promise<ToolResult> {
  if (!name || !file) throw new Error('name et file sont obligatoires')
  const text: string = await request('llm.skill.file', { name, file })
  return ok(text, `${name}/${file}`)
}

// ---------- IDE ----------

async function showFile(p: string, line?: number, endLine?: number): Promise<ToolResult> {
  if (!p) throw new Error('path manquant')
  const abs = absPath(p)
  if (!line) {
    await openFile(abs)
    return ok(`${relPath(abs)} ouvert dans l’éditeur.`, `${relPath(abs)} ouvert`)
  }
  const doc = await loadDoc(abs)
  if (!doc) throw new Error(`${relPath(abs)} ne peut pas être ouvert (binaire, trop gros ou absent)`)
  const l1 = Math.min(Math.max(1, Math.floor(line)), doc.lineCount) - 1
  const l2 = Math.min(Math.max(l1 + 1, Math.floor(endLine || line)), doc.lineCount) - 1
  await openFile({ path: abs, offset: doc.lineStart(l1), end: endLine ? doc.lineEnd(l2) : doc.lineStart(l1) })
  const range = endLine ? `lignes ${l1 + 1}-${l2 + 1}` : `ligne ${l1 + 1}`
  return ok(`${relPath(abs)} ouvert dans l’éditeur, ${range}.`, `${relPath(abs)} · ${range}`)
}

const leftIds = ['explorer', 'search', 'git', 'connections']
const rightIds = ['database', 'assistant', 'structure', 'conflicts', 'extensions', 'properties']

async function focus(a: Record<string, any>): Promise<ToolResult> {
  switch (a.target) {
    case 'file': {
      if (!a.path) throw new Error('path manquant')
      await openFile(absPath(a.path))
      return ok(`${relPath(absPath(a.path))} au premier plan.`, relPath(absPath(a.path)))
    }
    case 'panel': {
      const id = String(a.panel ?? '')
      if (leftIds.includes(id)) mutate((s) => (s.left.panel = id))
      else if (rightIds.includes(id)) mutate((s) => (s.right.panel = id))
      else throw new Error(`panneau inconnu : ${id} (${[...leftIds, ...rightIds].join(', ')})`)
      return ok(`Panneau ${id} affiché.`, `panneau ${id}`)
    }
    case 'console': {
      const c = consoles().find((x) => x.id === a.console_id)
      if (!c) throw new Error(`console introuvable : ${a.console_id} (voir list_consoles)`)
      mutate((s) => {
        s.bottom.open = true
        s.bottom.active = c.id
      })
      return ok(`Console « ${c.title} » affichée.`, `console ${c.title}`)
    }
    case 'problems':
      mutate((s) => {
        s.bottom.open = true
        s.bottom.active = 'problems'
      })
      return ok('Liste des problèmes affichée.', 'problèmes')
  }
  throw new Error('target doit valoir file, panel, console ou problems')
}

// ---------- consoles ----------

/** Terminal output as plain text: escape sequences removed, carriage returns applied. */
export function plainOutput(raw: string): string {
  const text = raw
    // eslint-disable-next-line no-control-regex
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
    // eslint-disable-next-line no-control-regex
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
    // eslint-disable-next-line no-control-regex
    .replace(/\x1b[()][A-Za-z0-9]|\x1b[=>78NOM]/g, '')
    .replace(/\r\n/g, '\n')
  return text
    .split('\n')
    .map((l) => (l.includes('\r') ? l.slice(l.lastIndexOf('\r', l.length - 2) + 1).replace(/\r$/, '') : l))
    .join('\n')
}

function decodeB64(s: string) {
  return new TextDecoder().decode(Uint8Array.from(atob(s), (c) => c.charCodeAt(0)))
}

async function consoleText(id: string): Promise<{ text: string; info: any }> {
  const r = await request('console.attach', { id })
  return { text: plainOutput(decodeB64(r.data)), info: r.info }
}

function tail(text: string, lines: number, chars = 12_000) {
  let t = text.replace(/\s+$/, '')
  const all = t.split('\n')
  let cut = false
  if (all.length > lines) {
    t = all.slice(-lines).join('\n')
    cut = true
  }
  if (t.length > chars) {
    t = t.slice(-chars)
    cut = true
  }
  return (cut ? '… (début coupé)\n' : '') + t
}

const exits = new Map<string, number>()
onPod('console.exit', (e: { id: string; code: number }) => exits.set(e.id, e.code))

async function runCommand(command: string, cwd?: string, timeout?: number): Promise<ToolResult> {
  if (!command.trim()) throw new Error('command manquante')
  const limit = Math.min(Math.max(Number(timeout) || 20, 1), 600) * 1000
  const info = await request('console.create', {
    kind: 'task',
    title: command.length > 60 ? command.slice(0, 57) + '…' : command,
    command: ['sh', '-c', command],
    cwd: cwd ? absPath(cwd) : undefined,
    cols: 160,
    rows: 40,
  })
  setConsoleList([...consoles().filter((c) => c.id !== info.id), info])
  mutate((s) => {
    s.bottom.open = true
    s.bottom.active = info.id
  })
  const until = Date.now() + limit
  while (!exits.has(info.id) && Date.now() < until) await new Promise((r) => setTimeout(r, 150))
  const { text, info: now } = await consoleText(info.id)
  const code = exits.get(info.id) ?? (now?.exited ? now.code : undefined)
  const out = tail(text, 300)
  if (code === undefined) {
    return ok(`La commande tourne toujours (console ${info.id}). Sortie jusqu’ici :\n${out}\n\nSuivre avec read_console, interagir avec console_input.`, `${command} : toujours en cours`)
  }
  exits.delete(info.id)
  return {
    content: `Code de sortie : ${code} (console ${info.id})\n${out || '(aucune sortie)'}`,
    summary: `code ${code}`,
    status: code === 0 ? 'ok' : 'error',
  }
}

function listConsoles(): ToolResult {
  const list = consoles()
  const rows = list.map((c) => `${c.id} · ${c.kind === 'task' ? 'commande' : 'terminal'} · ${c.title}${c.exited ? ` · terminée (code ${c.code})` : ' · en cours'}${session.bottom.active === c.id ? ' · affichée' : ''}`)
  return ok(rows.join('\n') || 'Aucune console ouverte.', `${list.length} console${list.length > 1 ? 's' : ''}`)
}

async function readConsole(id: string, lines?: number): Promise<ToolResult> {
  if (!id) throw new Error('console_id manquant')
  const { text, info } = await consoleText(id)
  const state = info?.exited ? `terminée (code ${info.code})` : 'en cours'
  return ok(`Console « ${info?.title ?? id} », ${state} :\n${tail(text, Math.max(1, Number(lines) || 200)) || '(aucune sortie)'}`, `${info?.title ?? id} · ${state}`)
}

async function consoleInput(id: string, text: string, enter: boolean): Promise<ToolResult> {
  if (!consoles().some((c) => c.id === id)) throw new Error(`console introuvable : ${id}`)
  await request('console.input', { id, data: text + (enter ? '\r' : '') })
  await new Promise((r) => setTimeout(r, 800))
  const { text: out } = await consoleText(id)
  return ok(`Texte envoyé. Fin de la sortie :\n${tail(out, 40)}`, `saisie dans ${id}`)
}

// ---------- bash ----------

async function bash(command: string, cwd?: string, timeout?: number, signal?: AbortSignal): Promise<ToolResult> {
  if (!command.trim()) throw new Error('command manquante')
  const r = await request('exec.run', { command, cwd: cwd ? absPath(cwd) : '', timeout: Number(timeout) || 120 }, signal)
  const secs = (r.durationMs / 1000).toFixed(1)
  const out = plainOutput(r.output ?? '').replace(/\s+$/, '')
  const state = r.canceled ? 'annulée' : r.timedOut ? `arrêtée après le délai (${secs} s)` : `code de sortie ${r.code}`
  return {
    content: `${state[0].toUpperCase() + state.slice(1)}${r.truncated ? ' (sortie coupée au milieu)' : ''}\n${out || '(aucune sortie)'}`,
    summary: r.timedOut ? 'délai dépassé' : r.canceled ? 'annulée' : `code ${r.code} · ${secs} s`,
    status: r.code === 0 && !r.timedOut && !r.canceled ? 'ok' : 'error',
  }
}
