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
  globalPlanPrompt: string | null
  projectPlanPrompt: string | null
  files: InstructionFile[]
  skills: Skill[]
}

export const DEFAULT_TEMPLATE = `Tu es l'assistant de programmation intégré à un IDE web. Projet ouvert : « {{project}} », racine {{root}}{{host}}.
{{activeFile}}
Réponds dans la langue de l'utilisateur, en Markdown. Les blocs de code indiquent leur langage (\`\`\`go, \`\`\`ts…). Pour un schéma, utilise un bloc \`\`\`mermaid.

{{tools}}`

export const DEFAULT_PLAN_TEMPLATE = `Tu es l'assistant de programmation intégré à un IDE web, en **mode Plan**. Projet ouvert : « {{project}} », racine {{root}}{{host}}.
{{activeFile}}
En mode Plan, tu ne modifies rien : tu explores le projet, tu poses des questions si la demande est ambiguë, puis tu proposes un plan.
Le plan est précis et actionnable : objectif, fichiers concernés (chemins), étapes numérotées avec ce qui change, risques et points à vérifier, comment tester. Quand il est prêt, présente-le avec l'outil exit_plan_mode : l'utilisateur pourra l'accepter pour passer en mode Build et l'exécuter.
Réponds dans la langue de l'utilisateur, en Markdown. Pour un schéma, utilise un bloc \`\`\`mermaid.

{{tools}}`

export const PLAN_TOOLS_TEXT = `Outils disponibles en lecture : list_dir, find_files, read_file, search_text, les serveurs de langage (lsp_symbols, lsp_workspace_symbols, lsp_definition, lsp_references, lsp_hover, lsp_diagnostics), open_file et focus pour montrer quelque chose à l'utilisateur, bash pour des commandes de lecture (ls, grep, git log, git diff… : une commande qui modifie quelque chose demande l'accord de l'utilisateur). edit_file et write_file sont indisponibles en mode Plan.
Dans les messages de l'utilisateur, @chemin désigne un fichier ou un dossier du projet (chemin relatif à la racine).
Quand une tâche est terminée ou que la conversation devient longue, tu peux la résumer avec compact_conversation.
Kanban du projet : kanban_list et kanban_get pour lire les tickets, kanban_create pour en créer un. ask_user pose à l'utilisateur des questions à choix (jusqu'à 10) quand une information te manque.`

export const TOOLS_TEXT = `Tu as des outils pour explorer et modifier le projet : list_dir, find_files, read_file, search_text, edit_file, write_file ; les serveurs de langage (lsp_symbols, lsp_workspace_symbols, lsp_definition, lsp_references, lsp_hover, lsp_diagnostics) ; bash pour exécuter tes commandes (tests, compilation, git…) ; l'IDE (open_file pour montrer un fichier à l'utilisateur, focus pour afficher un panneau ou une console) ; les consoles visibles par l'utilisateur (run_command pour un serveur de développement ou une commande qu'il doit suivre, list_consoles, read_console, console_input).
Dans les messages de l'utilisateur, @chemin désigne un fichier ou un dossier du projet (chemin relatif à la racine) : lis-le avec les outils si besoin.
Lis un fichier avant de le modifier. Préfère edit_file (remplacement exact et unique) à write_file pour changer un fichier existant. Les chemins sont relatifs à la racine du projet.
N'invente pas le contenu des fichiers : vérifie avec les outils. Après une modification, résume ce qui a changé.
Quand une tâche est terminée ou que la conversation devient longue, tu peux la résumer avec compact_conversation pour libérer du contexte.
Kanban du projet : kanban_list et kanban_get pour lire les tickets, kanban_create pour en créer un. ask_user pose à l'utilisateur des questions à choix (jusqu'à 10) quand une information te manque ou qu'un choix lui revient.`

export const [promptContext, setPromptContext] = createSignal<PromptContext | null>(null)

export async function loadPromptContext(): Promise<PromptContext> {
  try {
    const c = await request<PromptContext>('llm.context')
    setPromptContext(c)
    return c
  } catch {
    const empty: PromptContext = { globalPrompt: null, projectPrompt: null, globalPlanPrompt: null, projectPlanPrompt: null, files: [], skills: [] }
    setPromptContext(empty)
    return empty
  }
}

export function templateOf(c: PromptContext | null, mode: 'plan' | 'build' = 'build'): { text: string; source: 'project' | 'global' | 'default' } {
  const project = mode === 'plan' ? c?.projectPlanPrompt : c?.projectPrompt
  const global = mode === 'plan' ? c?.globalPlanPrompt : c?.globalPrompt
  if (project?.trim()) return { text: project, source: 'project' }
  if (global?.trim()) return { text: global, source: 'global' }
  return { text: mode === 'plan' ? DEFAULT_PLAN_TEMPLATE : DEFAULT_TEMPLATE, source: 'default' }
}

function displayPath(f: InstructionFile) {
  return f.scope === 'project' ? relPath(f.path) : f.path.replace(/^\/home\/[^/]+/, '~')
}

/** Final system prompt. tools: whether the model receives the tools. */
export function buildSystemPrompt(c: PromptContext | null, tools: boolean, mode: 'plan' | 'build' = 'build'): string {
  const p = project()
  const active = activeTab()?.kind === 'file' ? activeTab()!.path! : ''
  const vars: Record<string, string> = {
    project: p?.name ?? '',
    root: root(),
    host: p?.ssh ? ` sur l'hôte SSH ${p.ssh.host}` : '',
    activeFile: active ? `Fichier actif dans l'éditeur : ${relPath(active)}.` : '',
    date: new Date().toLocaleDateString('fr-FR', { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' }),
    tools: tools ? (mode === 'plan' ? PLAN_TOOLS_TEXT : TOOLS_TEXT) : '',
  }
  let text = templateOf(c, mode).text.replace(/\{\{(\w+)\}\}/g, (m, k) => (k in vars ? vars[k] : m))
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
