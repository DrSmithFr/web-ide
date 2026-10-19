// The AI plugin's door to the core: the api given at activation, and the parts of it the
// assistant and the Agents tool use by name. Nothing here imports the core itself (types apart).
import type { JSX } from 'solid-js'
import type { PluginApi } from '../api'
import type { Capabilities } from '../capabilities'

export let api: PluginApi

// The events subscribed while the modules load, before the activation.
const early: [string, (data: any, from?: string) => void][] = []

export function bindApi(a: PluginApi) {
  api = a
  for (const [event, f] of early.splice(0)) api.on(event, f)
}

type Fn<K extends (...args: any[]) => any> = (...a: Parameters<K>) => ReturnType<K>
const bind = <K extends (...args: any[]) => any>(get: () => K): Fn<K> => (...a) => get()(...a)

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const tn: PluginApi['tn'] = (...a) => api.tn(...a)
export const fmtSize: PluginApi['fmtSize'] = (...a) => api.fmtSize(...a)
export const podPath: PluginApi['podPath'] = (...a) => api.podPath(...a)

/** Events of the pod; those subscribed before the activation are kept until it. */
export function on<T = any>(event: string, f: (data: T, from?: string) => void): () => void {
  if (api) return api.on(event, f)
  early.push([event, f])
  return () => {}
}
export const onPod = on
export const request = <T = any>(method: string, params?: any, signal?: AbortSignal): Promise<T> => api.rpc<T>(method, params, signal)
export const requestIn = <T = any>(project: string | undefined, method: string, params?: any): Promise<T> => api.rpcIn<T>(project, method, params)
export const capability = <K extends keyof Capabilities>(name: K) => api.use(name)

// The window and its project.
export const detached = () => api.window.detached()
export const home = () => (api.project.id() ? { id: api.project.id()! } : undefined)
export const project = () => api.project.info()
export const root = () => api.project.root()
export const relPath = (p: string) => api.project.relPath(p)
export const attachedIds = () => api.project.attached()
export const rootOf = (id: string) => api.project.rootOf(id)
export const resolvePath = (p: string, base?: string) => api.project.resolve(p, base)
export const activeFile = () => api.activeFile()
export const openFile: PluginApi['openFile'] = (...a) => api.openFile(...a)
export const openTextTab: PluginApi['openText'] = (...a) => api.openText(...a)
export const phone = () => api.tiles.phone()
export const tabSize = () => api.theme.tabSize()
export const pluginAssistantOptions = () => api.plugins.assistantOptions()

// The editor kit, the keymap.
export const registerAction = bind(() => api.keys.register)
export const shortcutOf = bind(() => api.keys.shortcut)
export const lineHunks = bind(() => api.editor.lineHunks)
export const newDoc = (...a: ConstructorParameters<PluginApi['editor']['Doc']>) => new api.editor.Doc(...a)
export const newEditorView = (...a: ConstructorParameters<PluginApi['editor']['View']>) => new api.editor.View(...a)

// The interface.
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const Modal = (p: Parameters<PluginApi['ui']['Modal']>[0]): JSX.Element => api.ui.Modal(p)
export const Markdown = (p: Parameters<PluginApi['ui']['Markdown']>[0]): JSX.Element => api.ui.Markdown(p)
export const Switch = (p: Parameters<PluginApi['ui']['Switch']>[0]): JSX.Element => api.ui.Switch(p)
export const HelpLink = (p: Parameters<PluginApi['ui']['HelpLink']>[0]): JSX.Element => api.ui.HelpLink(p)
export const WorktreeChip = (p: Parameters<PluginApi['ui']['WorktreeChip']>[0]): JSX.Element => api.ui.WorktreeChip(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const pick: PluginApi['ui']['pick'] = (...a) => api.ui.pick(...a)
export const fuzzy: PluginApi['ui']['fuzzy'] = (...a) => api.ui.fuzzy(...a)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const copyText: PluginApi['ui']['copyText'] = (...a) => api.ui.copyText(...a)
export const parseAnsi: PluginApi['ui']['parseAnsi'] = (...a) => api.ui.parseAnsi(...a)
export const openDiagram: PluginApi['ui']['openDiagram'] = (...a) => api.ui.openDiagram(...a)

// The questions to the user (the core's Questions tool shows them too).
export const AskCard = (p: Parameters<PluginApi['questions']['AskCard']>[0]): JSX.Element => api.questions.AskCard(p)
export const QuestionForm = (p: Parameters<PluginApi['questions']['Form']>[0]): JSX.Element => api.questions.Form(p)
export const formAnswerText: PluginApi['questions']['formAnswerText'] = (...a) => api.questions.formAnswerText(...a)
export const answerStrings: PluginApi['questions']['answerStrings'] = (...a) => api.questions.answerStrings(...a)
export const showsQuestions: PluginApi['questions']['showsQuestions'] = (...a) => api.questions.showsQuestions(...a)

export type { Doc, EditorView, MenuItem } from '../api'
export type { Preview, PreviewSpec, TicketRole, TicketsService } from '../capabilities'
export type { AnswerEntry, Question, QuestionOption, QuestionType } from '../../questions/ask'
export type { FormSchema } from '../../questions/state'
export type { UiResult as CoreResult } from '../../ui/uiCalls'
