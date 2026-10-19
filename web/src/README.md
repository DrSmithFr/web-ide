# `web/src`

The front end of the IDE, in the words of the [glossary](../../GLOSSARY.md): a window shows one project on one desk, made of tools in the tool zones around the editor area. State lives in module-level Solid signals and stores; everything the pod knows arrives through `pod/` (JSON-RPC and pushed events), and the pod is the only writer of shared data (files, sessions, conversations, tickets).

**The window**

```mermaid
flowchart LR
  main["main.tsx<br/>entry"]
  app["app<br/>base path, router, home"]
  pages["pages<br/>Home, ProjectPage"]
  tools["tools<br/>right-panel tools"]
  popups["popups<br/>Search Everywhere…"]
  settings["settings<br/>settings modal"]
  conflict["conflict<br/>merge dialog"]
  editor["editor<br/>buffer and view"]
  main --> app
  main --> conflict
  main -->|"renders"| pages
  main --> settings
  pages --> app
  pages --> conflict
  pages --> popups
  pages --> settings
  pages -->|"zones"| tools
  tools --> conflict
  tools --> editor
  popups --> editor
  settings -->|"base path"| app
  settings --> editor
  conflict --> editor
```

**Tools and AI**

```mermaid
flowchart LR
  pages["pages"]
  tools["tools"]
  kanban["kanban<br/>board, tickets"]
  llm["llm<br/>assistant"]
  agents["agents<br/>Agents tool"]
  questions["questions<br/>Questions tool"]
  plugins["plugins<br/>Exec, Docker, Database, the api"]
  editor["editor"]
  pages --> agents
  pages -->|"capabilities"| plugins
  pages --> plugins
  pages --> kanban
  pages --> llm
  pages --> questions
  tools --> agents
  tools -->|"capabilities"| plugins
  tools --> editor
  kanban -->|"ticket sessions"| agents
  kanban -->|"capabilities"| plugins
  kanban --> llm
  llm -->|"capabilities"| plugins
  llm -->|"assistant options"| plugins
  llm --> editor
  llm -->|"tickets"| kanban
  llm -->|"ask_user"| questions
  agents --> editor
  agents -->|"MessageBox"| llm
  agents --> questions
  questions --> llm
  classDef other stroke-dasharray: 4 3
  class pages,tools other
```

Diagram edges are imports of the folders, checked with a script over the `import` lines. Nearly every folder also imports `state`, `pod`, `ui`, `keys` and `i18n`: those edges are not drawn. There is no strict layering: `ui` imports `llm`, `kanban`, `plugins` and `popups` (the editor area hosts them), `state` imports `console` and `settings`, and `llm` and `kanban` import each other. Modules therefore keep their top level free of calls into another folder.

## Components

- `main.tsx`: entry (mounts `pages`, wires the keys and the pod connection); `styles.css`: global styles and the themes' variables.
- [`app/`](app/): `base.ts` (the path the page runs under: empty on its own pod, `/_lan/pod/<key>` or `/pod/<key>` through a gateway), `router.ts` (`/`, `/project/:id`, `/project/:id/desk/:n`, detached panels), `home.ts` (one home page for the whole account)
- [`pod/`](pod/): The pod as seen by the page: `rpc.ts` (requests, pushed events, reconnection), `transport.ts` (WebSocket, or a MessagePort to the bootstrap page through a tunnel), `remote.ts` (requests to another pod from the home page), `power.ts`, `update.ts` (signed requests), `push.ts`
- [`state/`](state/): Open project and session (tabs, split tree, tool zones), settings, git state, folder marks, the phone layout (`mobile.ts`: the view shown, the keyboard, no zoom)
- [`pages/`](pages/): `Home`, `ProjectPage` (menu bar, rails, side panels, editor area, bottom tools), `ProjectBar`, `Detached` (one panel alone)
- [`ui/`](ui/): Editor area (it renders the file tabs with the `fileView` of Code), the text editors of the window (`textEditors.ts`), file type icons (`fileIcons.tsx`), ANSI colors of an output (`ansi.ts`), rendered Markdown files (`MarkdownView.tsx`) and the embedded docs (`DocView.tsx`, see `help/`), status bar and its desk switcher (`DeskSwitcher.tsx`), diff view (its sides from the diff sources of `diffSources.ts`), overlays (modal, prompt, pick list, context menu), toasts, icons, empty states, keyboard navigation of the toolbars and tab bars (`roving.ts`)
- [`editor/`](editor/): `Doc` (buffer, revisions, undo, file format, indentation), the completion widget's helpers (`completion.ts`: the items of the `language` service or the words of the buffer) and LSP text edits (`textEdits.ts`), `EditorView` (block rendering, Custom Highlight API, indentation guides, whitespace overlay, multiple carets, folding, soft wrap with rows read from the layout), `carets.ts` (words, occurrences), `folding.ts` (fold ranges), `indent.ts` (indentation detection), tokenizer and grammars, three-way merge, line diff, sub-word moves, find bar
- [`keys/`](keys/): Binding table and QWERTY / AZERTY presets
- [`help/`](help/): `topics.json`, the map from a place of the interface to a page of the docs (`path#heading`, checked by `make test`), and the `HelpLink` (**?**); the pages open in `doc` tabs (`ui/DocView.tsx`, `state/docs.ts`)
- [`popups/`](popups/): Search Everywhere (a shell: a tab per search provider of `searchProviders.ts`, the files, symbols and actions given by the core), Recent Files and the switcher, paste from history (the history itself: `ui/clipboard.ts`)
- [`tools/`](tools/): The Conflicts tool (`tools.tsx`) and `InfoTool`
- [`conflict/`](conflict/): Three-pane conflict dialog
- [`settings/`](settings/): Settings modal and its pages: `Members`, `Lan`, `Pods`, `Sync`, `Notifications`, `Plugins` (with a project open) (remote access and account), themes (`themes.ts`)
- [`llm/`](llm/): Assistant: state, turn loop, tools, prompt, Markdown, the editor of the message box shared with the Agents tool (`MessageBox.tsx`), attachments, speech recognition, sub-agents (`SubAgents.tsx`), doodles (`llm/doodle/`: document model, SVG rendering, export, modal), the board of a conversation (`llm/board/`), app previews (`PreviewCard.tsx`, through the previews capability)
- [`agents/`](agents/): Agents tool: sessions of the worktrees of the window and their threads (`state.ts`, events dropped when older than the thread loaded, by `seq`), tabs, launcher, thread, permission cards (`AgentsTool.tsx`), the follow mode (`state.ts`: an edit tool call completed opens its file when `follow` is set on the session, `agents.follow`), the message box of a session (`Composer.tsx`: `llm/MessageBox.tsx` with the agent's commands, attachments prepared by `llm/attachments.ts` sent as parts, drafts per session)
- [`questions/`](questions/): Questions tool: the list (`state.ts`, asked again on `questions.changed`), the modal one question at a time and its auto-open (not for a question whose thread is on screen: `showsQuestions`), the forms of `elicitation/create` (`Form.tsx`, also in the Agents thread)
- [`plugins/`](plugins/): The plugins of the window ([plugins.md](../../docs/design/plugins.md)): `registry.ts` (the states sent by the pod in `project.open` and `plugins.changed`, the page modules of the built-ins activated once when first enabled, their tools, tab views `plugin:<id>/<view>` and commands), `api.ts` (the `api` a module gets: `rpc` to its backend, events, `openFile`, `openView`, port forwards and previews of the core, `ui`), `types.ts`, `capabilities.ts` (capabilities as services: the types of `terminal`, `commands` and `git`, `provide` by a plugin, `capability()` for the core, `undefined` while the provider is off); one folder per built-in: `files/` (locked: the Explorer, the Files tab of Search Everywhere, the files service), `code/` (locked: `FileEditor.tsx`, the view of the file tabs), `languages/` (the language servers: the client and the code actions `client.ts`, rename and formatting `refactor.ts`, the completion of the servers, the buffers announced to them `sync.ts`, the Problems and Structure tools, the Symbols provider; it provides the `language` service), `previews/` (the Port forwards and previews tool, the previews service `previews.ts`, the open forwards of the home page `OpenForwards.tsx`), `git/` (the Git tool: tabs, tree of changes, graph lanes `graph.ts`, commit detail; the git state `state.ts`, which is the `git` service; the git diff source `diff.tsx`), `search/` (the Search tool `GlobalSearch.tsx`, the text provider `text.ts`), `exec/` (the Console tool: terminals and tasks `consoles.tsx`, one xterm per console; the saved commands `commands.ts`, `CommandsModal.tsx`; it provides the `terminal` and `commands` services), `kanban/` (the Kanban tool, the board, ticket, milestone and objective views, whose number is their tab state; workflow actions, linked conversations, the New ticket host; it provides the `tickets` service, which the assistant, the Orchestrator and the project bar use; it starts the conversations and agent sessions through the `assistant` and `agents` services, the core's until AI is a plugin: `coreServices.ts`), `docker/` (the Docker tool: lists and polling `state.ts`, detail pane, logs with ANSI colors, disk usage; `plugin.ts` holds its `api` and its desk state, nothing else of the core is imported) `database/` (the Database explorer: connections `connections.ts`, tree, the console and table views `views.ts` whose state, the text of a console included, goes with the tab) and `hello/` (the test plugin). The tools of the enabled plugins join `toolPanels` (`pages/ProjectPage.tsx`) and the tiles; their default zone comes from `state/zones.ts` (`setPluginZones`), which keeps the place of a disabled one
- [`i18n/`](i18n/): `t()` and the catalogs (English source strings, `languages.json`, one catalog per other language)

## Invariants and pitfalls

- **Solid empties a container** whose only child is dynamic: a node mounted by hand (the editor view) needs its own element (`.editor-mount`).
- **View callbacks run under `untrack`** (`useEditorView`), otherwise an effect calling `setSelection` subscribes to what the callback reads.
- **Editor blocks use `width: max-content`**, otherwise lengthening the longest line re-lays out the whole file.
- **Editor rows are not lines** once something is folded: positions go through `lineTop` / `lineAtY` (`rowOf`, `lineOfRow`), never `line * lineHeight`. Folding splits a block (`splitText` then a move to a new element), which loses the DOM selection: it is set again afterwards.
- **The editor overlays share the global CSS**: a class such as `.tab` (editor tabs) also styles an element of the whitespace overlay; overlay elements use short editor-specific names.
- **The browser draws no caret at the end of a non-empty selection**: with several carets the editor hides the native caret and draws them all.
- **`caretPositionFromPoint` at the left edge** of the text can land in the gutter: `offsetAt` keeps the point inside the content.
- **No `push(...bigArray)`** (stack overflow on large pastes): copy in a loop.
- **A Solid store merges objects**: `setChat('running', {})` clears nothing; write `{ stream: undefined }`.
- **DOMPurify** drops attributes containing `-->` (Mermaid sources are stored URI-encoded) and HTML inside `foreignObject` (Mermaid uses `htmlLabels: false`).
- **Copy through `copyText`** (`ui/clipboard.ts`), not `navigator.clipboard.writeText`: the text then goes to the clipboard history. A popup gives the focus back with `keepFocus` (`state/focus.ts`): focusing the editor again puts its caret at the start otherwise.
- **Side buttons of the mouse** (`keys/mouse.ts`): cancelled on `mouseup` (Chromium goes back in the browser history otherwise) and sent to the focused part (`focusPart`, which they do not change): `navigate` of `state/project.ts` (positions and tabs, recorded by `openFile`, `openTab` and `activateTab`), `navigateConsoles`, `navigateChats`.

## Tests

`cd web && npm run check` (tsc and the catalogs); browser suites ([../../e2e/README.md](../../e2e/README.md)): `editing`, `editor`, `features`, `explorer`, `popups`, `keyboard`, `mousenav`, `mobile`, `desks`, `llm`, `kanban`, `perf` and the others by tool.
