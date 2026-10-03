// System prompt of the assistant: an editable template (global, or per project in
// .ide/system-prompt.md), then the memory files (CLAUDE.md, AGENTS.md…) and the list of
// skills, loaded the same way as Claude Code does.
import { createSignal } from 'solid-js'
import { request } from '../pod/rpc'
import { activeTab, project, relPath, root } from '../state/project'

export interface InstructionFile {
  scope: 'global' | 'project'
  path: string
  content: string
}

export interface Skill {
  scope: 'global' | 'project'
  name: string
  description: string
  dir: string
}

export interface PromptContext {
  globalPrompt: string | null
  projectPrompt: string | null
  files: InstructionFile[]
  skills: Skill[]
}

export const DEFAULT_TEMPLATE = `Tu es l'assistant de programmation intégré à un IDE web. Projet ouvert : « {{project}} », racine {{root}}{{host}}.
{{activeFile}}
Réponds dans la langue de l'utilisateur, en Markdown. Les blocs de code indiquent leur langage (\`\`\`go, \`\`\`ts…). Pour un schéma, utilise un bloc \`\`\`mermaid.

{{tools}}`

export const TOOLS_TEXT = `Tu as des outils pour explorer et modifier le projet : list_dir, find_files, read_file, search_text, edit_file, write_file ; les serveurs de langage (lsp_symbols, lsp_workspace_symbols, lsp_definition, lsp_references, lsp_hover, lsp_diagnostics) ; bash pour exécuter tes commandes (tests, compilation, git…) ; l'IDE (open_file pour montrer un fichier à l'utilisateur, focus pour afficher un panneau ou une console) ; les consoles visibles par l'utilisateur (run_command pour un serveur de développement ou une commande qu'il doit suivre, list_consoles, read_console, console_input).
Dans les messages de l'utilisateur, @chemin désigne un fichier ou un dossier du projet (chemin relatif à la racine) : lis-le avec les outils si besoin.
Lis un fichier avant de le modifier. Préfère edit_file (remplacement exact et unique) à write_file pour changer un fichier existant. Les chemins sont relatifs à la racine du projet.
N'invente pas le contenu des fichiers : vérifie avec les outils. Après une modification, résume ce qui a changé.`

export const [promptContext, setPromptContext] = createSignal<PromptContext | null>(null)

export async function loadPromptContext(): Promise<PromptContext> {
  try {
    const c = await request<PromptContext>('llm.context')
    setPromptContext(c)
    return c
  } catch {
    const empty: PromptContext = { globalPrompt: null, projectPrompt: null, files: [], skills: [] }
    setPromptContext(empty)
    return empty
  }
}

export function templateOf(c: PromptContext | null): { text: string; source: 'project' | 'global' | 'default' } {
  if (c?.projectPrompt?.trim()) return { text: c.projectPrompt, source: 'project' }
  if (c?.globalPrompt?.trim()) return { text: c.globalPrompt, source: 'global' }
  return { text: DEFAULT_TEMPLATE, source: 'default' }
}

function displayPath(f: InstructionFile) {
  return f.scope === 'project' ? relPath(f.path) : f.path.replace(/^\/home\/[^/]+/, '~')
}

/** Final system prompt. tools: whether the model receives the tools. */
export function buildSystemPrompt(c: PromptContext | null, tools: boolean): string {
  const p = project()
  const active = activeTab()?.kind === 'file' ? activeTab()!.path! : ''
  const vars: Record<string, string> = {
    project: p?.name ?? '',
    root: root(),
    host: p?.ssh ? ` sur l'hôte SSH ${p.ssh.host}` : '',
    activeFile: active ? `Fichier actif dans l'éditeur : ${relPath(active)}.` : '',
    date: new Date().toLocaleDateString('fr-FR', { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' }),
    tools: tools ? TOOLS_TEXT : '',
  }
  let text = templateOf(c).text.replace(/\{\{(\w+)\}\}/g, (m, k) => (k in vars ? vars[k] : m))
  text = text.replace(/\n{3,}/g, '\n\n').trim()
  const files = c?.files ?? []
  if (files.length) {
    text += '\n\n# Instructions'
    text += "\nInstructions de l'utilisateur (globales) et du projet. Elles priment sur tes habitudes ; celles du projet priment sur les globales."
    for (const f of files) text += `\n\n## ${f.scope === 'global' ? 'Global' : 'Projet'} · ${displayPath(f)}\n${f.content.trim()}`
  }
  const skills = c?.skills ?? []
  if (skills.length && tools) {
    text += '\n\n# Skills'
    text += "\nCompétences disponibles. Quand une demande correspond à l'une d'elles, charge ses instructions avec load_skill(name) avant d'agir ; read_skill_file lit ses autres fichiers."
    for (const s of skills) text += `\n- ${s.name} : ${s.description || '(sans description)'}`
  }
  return text
}
