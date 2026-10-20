// The API a plugin's page module receives (docs/design/plugins.md): the only door to the core,
// so that a built-in plugin uses nothing an external one could not.
import { notifyIn, notifyOn, on, openChannel, request, requestIn, type Channel } from '../pod/rpc'
import { fmtAgo, fmtDate, fmtSize, t, tn } from '../i18n'
import { podPath } from '../app/base'
import { Markdown } from '../ui/Markdown'
import { Switch } from '../ui/Switch'
import { openDiagram } from '../ui/DiagramViewer'
import { resolvePath } from '../ui/uiCalls'
import { route } from '../app/router'
import { openSettings } from '../settings/SettingsModal'
import { AskCard } from '../questions/AskCard'
import { answerStrings } from '../questions/ask'
import { formAnswerText, QuestionForm } from '../questions/Form'
import { showsQuestions } from '../questions/state'
import { HelpLink } from '../help/HelpLink'
import { FileIcon } from '../ui/fileIcons'
import { activeTab, attached, basename, closeTab, docError, leaves, showWorktree, docsVersion, fileUri, loadDoc, reveal, saveCursor, saveDoc, findLeaf, getDoc, home, onBufferClose, onBufferOpen, onBufferSave, openBuffers, pathFromUri, isLocal, mutate, openFile, openTab, openTextTab, project, relPath, root, session, setActivePane, type TabState } from '../state/project'
import { showTool } from '../state/zones'
import { detachTab, openTile, tileMode } from '../state/tiles'
import { phone } from '../state/mobile'
import { fontFor, settings } from '../state/settings'
import { accentOf, themeById } from '../settings/themes'
import { WorktreeChip } from '../ui/WorktreeChip'
import { dropClasses, dropIndex, moveItem, setDropAt } from '../ui/tabDrop'
import { EditorView } from '../editor/view'
import { capability, provide, type Capabilities } from './capabilities'
import { explorerItems, fileDecorations, pluginAssistantOptions } from './registry'
import { folderMark, inExcluded, loadFolderMarks, markFolder } from '../state/folders'
import { desks } from '../state/desks'
import { setDynamicActions } from '../keys/bindings'
import { inTunnel } from '../pod/transport'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { contextMenu, fuzzy, Modal, pick, prompt } from '../ui/overlay'
import { languageName, lspLanguage, lspLanguageId } from '../editor/languages'
import { isWordChar, wordStart } from '../editor/completion'
import { applyToString, toOffsets } from '../editor/textEdits'
import { activeTextEditor, registerTextEditor } from '../ui/textEditors'
import { toggleWrap } from '../ui/EditorArea'
import { useCompletion } from '../ui/Completion'
import { FindBar } from '../editor/FindBar'
import { isMarkdownPath, MarkdownView, showRaw, shownRaw } from '../ui/MarkdownView'
import { lineHunks, lineMarks } from '../editor/linediff'
import { openConflict } from '../conflict/ConflictDialog'
import { copyText } from '../ui/clipboard'
import { cursorInfo, setCursorInfo } from '../ui/status'
import { useEditorView } from '../ui/EditorArea'
import { Doc } from '../editor/doc'
import { actionOf, registerAction, runAction, shortcutOf } from '../keys/bindings'
import { parseAnsi, stripAnsi } from '../ui/ansi'

/** The version of this API (the pod refuses a manifest of another one): 1, frozen in #118. */
export const API_VERSION = 1

export function makeApi(id: string) {
  const full = (method: string) => (method.includes('.') ? method : `${id}.${method}`)
  return {
    id,
    /**
     * Calls a method of the plugin's backend, in the project of the window: <id>.<method>, or the
     * method as named when it has a prefix (one of the namespaces of the manifest: console.create).
     */
    rpc: <T = any>(method: string, params?: any, signal?: AbortSignal) => request<T>(full(method), params, signal),
    /** The same in a worktree attached to the window, and a notification without answer. */
    rpcIn: <T = any>(project: string | undefined, method: string, params?: any) => requestIn<T>(project, full(method), params),
    notify: (method: string, params?: any) => notifyIn(undefined, full(method), params),
    notifyIn: (project: string | undefined, method: string, params?: any) => notifyIn(project, full(method), params),
    /** A channel of its own in the tunnel (a terminal's input and output; null on a WebSocket). */
    channel: { open: openChannel, notify: (ch: Channel | null, method: string, params?: any, project?: string) => notifyOn(ch, full(method), params, project) },
    /** Events of the pod (fs.changed, git.changed, plugins.changed…); returns the unsubscribe. */
    on,
    t,
    tn,
    fmtDate,
    fmtAgo,
    fmtSize,
    /** The path of a page of the pod (the page may run under a gateway prefix). */
    podPath,
    openFile,
    /** The project of the window: local, or on an SSH host; id: its main project (events carry it). */
    project: {
      isLocal,
      id: () => home()?.id,
      /** The name of the project shown, and all it is (its parent, its ticket for a worktree). */
      name: () => project()?.name,
      info: project,
      /** Shows a worktree of the project in the window. */
      showWorktree,
      /** The worktree shown in the window (the project itself, or one of its worktrees), and its root. */
      current: () => project()?.id,
      root,
      /** The worktrees attached to the window (the project included). */
      attached: () => Object.keys(attached),
      /** The root folder of an attached worktree (undefined when it is not attached). */
      rootOf: (id: string) => attached[id]?.root,
      /** The absolute path of a path a model gives, from base (the root by default). */
      resolve: resolvePath,
      /** A path of the project as shown (relative to its root). */
      relPath,
      basename,
      /** The file:// URI of a path and back (the language servers). */
      fileUri,
      pathFromUri,
    },
    /** Capabilities as services: the plugin's own (provide) and those of the others (use). */
    provide: <K extends keyof Capabilities>(name: K, impl: Capabilities[K]) => provide(id, name, impl),
    use: capability,
    /** Shows one of the plugin's tools in its zone. */
    showTool: (tool: string) => mutate((s) => showTool(s, tool)),
    /** Tiles of the Grid and Scroll modes, detached windows. */
    tiles: { active: tileMode, phone, open: openTile, detach: detachTab },
    /** The look of the IDE for what draws itself (a terminal): its font and colors. */
    theme: {
      font: () => ({ family: settings.font.family, size: fontFor().size }),
      terminal: () => {
        const th = themeById(settings.theme)
        const ui = th.ui
        return { background: ui['bg-2'], foreground: ui.fg, cursor: accentOf(th, settings.accent)[0], selectionBackground: ui['sel-bg'], black: ui['bg-3'] }
      },
      tabSize: () => settings.editor.tabSize,
    },
    /** The buffers of the window: loaded, saved, forgotten (a language server follows them). */
    buffers: {
      list: openBuffers,
      get: getDoc,
      version: docsVersion,
      /** Loads the buffer of a file (once per window), its error, saves it. */
      load: loadDoc,
      error: docError,
      save: saveDoc,
      /** The cursor of a file in the desk, and its saving; the target asked by openFile (line, selection). */
      cursor: (path: string) => session.cursors[path],
      saveCursor,
      target: reveal,
      onOpen: onBufferOpen,
      onSave: onBufferSave,
      onClose: onBufferClose,
    },
    /** The resolution of a conflict of a buffer with the disk. */
    conflicts: { open: openConflict },
    /** The file of the active tab, if it is a file; the path of the active tab (a diff tab too). */
    activeFile: () => (activeTab()?.kind === 'file' ? activeTab()!.path : undefined),
    activePath: () => activeTab()?.path,
    /** The files opened lately on the desks of the project, the last first. */
    recentFiles: () => desks.recent,
    /** The marks of the folders of the project (source, tests, excluded), kept by the pod. */
    folders: { mark: folderMark, inExcluded, setMark: markFolder, load: loadFolderMarks },
    /** What the other plugins give the Explorer: menu items, decorations. */
    explorer: { items: explorerItems, decorations: fileDecorations },
    /** The files of the project, as the pod reads them (from the disk, not the buffers). */
    files: {
      read: (path: string) => request<{ content: string; binary?: boolean; readOnly?: boolean }>('fs.read', { path }),
      write: (path: string, content: string) => request('fs.write', { path, content }),
    },
    /** The SSH settings of the pod (agent, keys), for a form offering an SSH tunnel. */
    ssh: { info: () => request('ssh.info') },
    /**
     * The plugin's state in the desk, saved with it (shown tab, choices…): get() reads it over
     * the defaults, set() changes it.
     */
    desk: <T extends Record<string, unknown>>(defaults: T) => ({
      get: (): T => ({ ...defaults, ...(session.plugins?.[id] as Partial<T> | undefined) }),
      // Changed in place in the desk (an array pushed is seen), created once from a copy of the
      // defaults (they are never shared, nor changed).
      set: (f: (s: T) => void) =>
        mutate((s) => {
          s.plugins ??= {}
          if (!s.plugins[id]) s.plugins[id] = structuredClone(defaults)
          const cur = s.plugins[id]
          for (const k of Object.keys(defaults)) if (!(k in cur)) cur[k] = structuredClone(defaults[k])
          f(cur as T)
        }),
    }),
    /** Opens a tab view of the plugin, with its serializable state; key tells two tabs apart. Returns the tab id. */
    openView: (view: string, opts: { title?: string; key?: string; state?: unknown } = {}) =>
      openTab({ kind: `plugin:${id}/${view}`, title: opts.title, key: opts.key, state: opts.state } as Omit<TabState, 'id'>),
    /** Changes the state of a tab of the plugin (saved in the desk, it goes with the tab). */
    setViewState: <T>(tabId: string, f: (s: T) => void) =>
      mutate((s) => {
        const tab = s.tabs[tabId]
        if (tab?.kind.startsWith(`plugin:${id}/`)) f((tab.state ??= {}) as T)
      }),
    /** A diff tab of the core's diff view, whose sides come from the plugin's diff source. */
    openDiff: (tab: Omit<TabState, 'id' | 'kind' | 'source'>) => openTab({ ...tab, kind: 'diff', source: id }),
    /** A read-only text in a tab (a DDL, a definition). */
    openText: openTextTab,
    /** The panes of the editor area: whether a tab is the one shown in the focused pane. */
    panes: {
      isActive: (paneId: string, tabId: string) => session.activePane === paneId && findLeaf(session.layout, paneId)?.active === tabId,
      activate: setActivePane,
      /** Closes a tab wherever it is. */
      closeTab: (tabId: string) => {
        for (const l of leaves()) if (l.tabs.includes(tabId)) closeTab(l.id, tabId, true)
      },
    },
    /** The text editor of the IDE, for a view editing a text of its own (a SQL console). */
    editor: {
      Doc,
      View: EditorView,
      useView: useEditorView,
      /** The caret of the active editor, shown in the status bar by Code: set by the view, read by its items. */
      setCursor: setCursorInfo,
      cursor: cursorInfo,
      /** The text editor of the active tab, with its buffer (null: none, or another editor has the focus). */
      active: activeTextEditor,
      /** A view of a file registers its editor, given while it is the active one. */
      register: registerTextEditor,
      /** The editing kit of the core: completion, find bar, rendered Markdown, wrap, change markers. */
      useCompletion,
      FindBar,
      MarkdownView,
      isMarkdownPath,
      showRaw,
      shownRaw,
      toggleWrap,
      lineMarks,
      /** The hunks between two texts (a diff of lines). */
      lineHunks,
      languageName,
      /** The language server language of a path (go, typescript…), and the languageId it is announced with. */
      serverLanguage: lspLanguage,
      serverLanguageId: lspLanguageId,
      /** Editor settings: indentation, trailing whitespace. */
      settings: () => settings.editor,
      /** Words of a buffer (a symbol at the caret), and LSP text edits as offsets. */
      wordStart,
      isWordChar,
      toOffsets,
      applyToString,
    },
    /** The keymap: a handler of an action while the view is shown, and the shortcut of an action. */
    /** The window: a detached window shows one tool alone (/project/<id>/tool/<tool>). */
    window: { detached: () => route().name === 'tool' },
    /** The Settings of the IDE, on a section (themes by default). */
    settings: { open: openSettings },
    /** The other plugins: their options of a conversation of the assistant, by plugin id. */
    plugins: { assistantOptions: pluginAssistantOptions },
    /** The questions to the user: the card of ask_user, the form of an agent, which ones a view shows already. */
    questions: { AskCard, Form: QuestionForm, formAnswerText, answerStrings, showsQuestions },
    keys: { register: registerAction, shortcut: shortcutOf, actionOf, run: runAction, setDynamic: setDynamicActions },
    /** The transport of the page: in the tunnel of the relay, or a WebSocket to the pod. */
    transport: { inTunnel },
    ui: { Markdown, Switch, openDiagram, HelpLink, fuzzy, EmptyState, Icon, FileIcon, Modal, toast, errorToast, contextMenu, prompt, pick, copyText, parseAnsi, stripAnsi, WorktreeChip, tabDrop: { dropClasses, dropIndex, moveItem, setDropAt } },
  }
}

export type PluginApi = ReturnType<typeof makeApi>
export type { MenuItem, PickItem } from '../ui/overlay'
export type { TabState }
export type { NavTarget } from '../state/project'
export type { Doc } from '../editor/doc'
export type { Diagnostic } from '../editor/view'
export type { MarkdownViewApi } from '../ui/MarkdownView'
export type { LspDiagnostic } from './capabilities'
export type { EditorView } from '../editor/view'
export type { CompletionItem, CompletionList } from '../editor/completion'
export type { TextEdit } from '../editor/textEdits'
export type { SearchHit, SearchProvider } from '../popups/searchProviders'
export type { DiffSource } from '../ui/diffSources'
export type { AnswerEntry, Question, QuestionOption, QuestionType } from '../questions/ask'
export type { FormSchema } from '../questions/state'
export type { UiResult } from '../ui/uiCalls'
