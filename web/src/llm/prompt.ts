// System prompt of the assistant, as the settings show it. The pod builds it (the agent runs
// there): an editable template (global, or per project in .ide/system-prompt.md), then the
// memory files (CLAUDE.md, AGENTS.md…) and the list of skills, loaded the same way as Claude
// Code does.
import { createSignal } from 'solid-js'
import { request } from '../pod/rpc'
import { activeTab, relPath } from '../state/project'
import type { Mode } from './state'

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
  globalBriefingPrompt: string | null
  projectBriefingPrompt: string | null
  files: InstructionFile[]
  skills: Skill[]
  /** Default template of each mode. */
  defaults: Record<Mode, string>
}

export const [promptContext, setPromptContext] = createSignal<PromptContext | null>(null)

export async function loadPromptContext(): Promise<PromptContext> {
  try {
    const c = await request<PromptContext>('llm.context')
    setPromptContext(c)
    return c
  } catch {
    const empty: PromptContext = {
      globalPrompt: null,
      projectPrompt: null,
      globalPlanPrompt: null,
      projectPlanPrompt: null,
      globalBriefingPrompt: null,
      projectBriefingPrompt: null,
      files: [],
      skills: [],
      defaults: { build: '', plan: '', briefing: '', orchestrator: '' },
    }
    setPromptContext(empty)
    return empty
  }
}

/** Templates of a mode stored by the user: project, then global (null when absent). */
export function storedTemplates(c: PromptContext | null, mode: Mode): { project: string | null; global: string | null } {
  if (mode === 'plan') return { project: c?.projectPlanPrompt ?? null, global: c?.globalPlanPrompt ?? null }
  if (mode === 'briefing') return { project: c?.projectBriefingPrompt ?? null, global: c?.globalBriefingPrompt ?? null }
  // The Orchestrator keeps its default template.
  if (mode === 'orchestrator') return { project: null, global: null }
  return { project: c?.projectPrompt ?? null, global: c?.globalPrompt ?? null }
}

export const defaultTemplate = (mode: Mode) => promptContext()?.defaults[mode] ?? ''

export function templateOf(c: PromptContext | null, mode: Mode = 'build'): { text: string; source: 'project' | 'global' | 'default' } {
  const { project, global } = storedTemplates(c, mode)
  if (project?.trim()) return { text: project, source: 'project' }
  if (global?.trim()) return { text: global, source: 'global' }
  return { text: c?.defaults[mode] ?? '', source: 'default' }
}

/** The final system prompt of a mode, as the pod would send it now. */
export function systemPrompt(mode: Mode): Promise<string> {
  const tab = activeTab()
  return request<string>('agent.prompt', { mode, activeFile: tab?.kind === 'file' && tab.path ? relPath(tab.path) : '' })
}
