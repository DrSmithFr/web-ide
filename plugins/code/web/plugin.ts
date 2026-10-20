// The Code plugin's door to the core: the api given at activation, and the parts of it the view
// of a file uses by name (the editing kit of the core, the buffers, the keymap). Nothing here
// imports the core itself.
import type { NavTarget, PluginApi } from '@ide/api'
import type { Capabilities } from '@ide/capabilities'

export let api: PluginApi

export function bindApi(a: PluginApi) {
  api = a
}

type Fn<K extends (...args: any[]) => any> = (...a: Parameters<K>) => ReturnType<K>
const bind = <K extends (...args: any[]) => any>(get: () => K): Fn<K> => (...a) => get()(...a)

export const t = bind(() => api.t)
export const Icon = bind(() => api.ui.Icon)
export const contextMenu = bind(() => api.ui.contextMenu)
export const prompt = bind(() => api.ui.prompt)
export const pick: PluginApi['ui']['pick'] = (...a) => api.ui.pick(...a)
export const toast = bind(() => api.ui.toast)
export const copyText = bind(() => api.ui.copyText)
export const capability = <K extends keyof Capabilities>(name: K) => api.use(name)
export const request = <T = any>(method: string, params?: any): Promise<T> => api.rpc<T>(method, params)
export const openFile = (target: NavTarget | string) => api.openFile(target)
export const relPath = bind(() => api.project.relPath)
export const basename = bind(() => api.project.basename)
export const getDoc = bind(() => api.buffers.get)
export const loadDoc = bind(() => api.buffers.load)
export const docError = bind(() => api.buffers.error)
export const saveDoc = bind(() => api.buffers.save)
export const saveCursor = bind(() => api.buffers.saveCursor)
export const docsVersion = bind(() => api.buffers.version)
export const reveal = bind(() => api.buffers.target)
export const setActivePane = bind(() => api.panes.activate)
export const registerAction = bind(() => api.keys.register)
export const shortcutOf = bind(() => api.keys.shortcut)
export const actionOf = bind(() => api.keys.actionOf)
export const runAction = bind(() => api.keys.run)
export const openConflict = bind(() => api.conflicts.open)
export const setCursorInfo = bind(() => api.editor.setCursor)
export const useEditorView = bind(() => api.editor.useView)
export const useCompletion = bind(() => api.editor.useCompletion)
export const toggleWrap = bind(() => api.editor.toggleWrap)
export const lineMarks = bind(() => api.editor.lineMarks)
export const languageName = bind(() => api.editor.languageName)
export const isMarkdownPath = bind(() => api.editor.isMarkdownPath)
export const showRaw = bind(() => api.editor.showRaw)
export const shownRaw = bind(() => api.editor.shownRaw)
export const FindBar = bind(() => api.editor.FindBar)
export const MarkdownView = bind(() => api.editor.MarkdownView)
export const tn = bind(() => api.tn)
export const cursor = bind(() => api.editor.cursor)
export const editorSettings = bind(() => api.editor.settings)
