# `web/src`

The front end of the IDE, in the words of the [glossary](../../GLOSSARY.md): a window shows one project on one desk, made of tools in the tool zones around the editor area. State lives in module-level Solid signals and stores; everything the pod knows arrives through `pod/` (JSON-RPC and pushed events), and the pod is the only writer of shared data (files, sessions, conversations, tickets).

**The window**

```mermaid
flowchart LR
  main["main.tsx<br/>entry"]
  app["app<br/>base path, router, home"]
  pages["pages<br/>Home, ProjectPage"]
  tools["tools<br/>right-panel tools"]
  panels["panels<br/>Explorer, search, Git"]
  popups["popups<br/>Search Everywhere…"]
  settings["settings<br/>settings modal"]
  conflict["conflict<br/>merge dialog"]
  lsp["lsp<br/>code navigation"]
  editor["editor<br/>buffer and view"]
  main --> app
  main --> conflict
  main -->|"renders"| pages
  main --> settings
  pages --> app
  pages --> conflict
  pages -->|"zones"| panels
  pages --> popups
  pages --> settings
  pages -->|"zones"| tools
  tools --> conflict
  tools --> editor
  tools --> lsp
  popups --> editor
  popups --> lsp
  popups --> panels
  settings -->|"base path"| app
  settings --> editor
  conflict --> editor
  lsp --> editor
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
  docker["docker"]
  console["console<br/>terminals, tasks"]
  db["db<br/>database explorer"]
  editor["editor"]
  lsp["lsp"]
  pages --> agents
  pages --> console
  pages --> db
  pages --> docker
  pages --> kanban
  pages --> llm
  pages --> questions
  tools --> agents
  tools --> console
  tools --> editor
  tools --> lsp
  kanban -->|"ticket sessions"| agents
  kanban --> console
  kanban --> llm
  llm --> console
  llm --> docker
  llm --> editor
  llm -->|"tickets"| kanban
  llm -->|"ask_user"| questions
  agents --> editor
  agents -->|"MessageBox"| llm
  agents --> questions
  questions --> llm
  docker --> console
  docker --> llm
  console --> editor
  console --> llm
  db --> editor
  lsp --> editor
  classDef other stroke-dasharray: 4 3
  class pages,tools other
```

Diagram edges are imports of the folders, checked with a script over the `import` lines. Nearly every folder also imports `state`, `pod`, `ui`, `keys` and `i18n`: those edges are not drawn. There is no strict layering: `ui` imports `llm`, `kanban`, `panels` and `popups` (the editor area hosts them), `state` imports `console` and `settings`, and `llm` and `kanban` import each other. Modules therefore keep their top level free of calls into another folder.

## Components

- `main.tsx`: entry (mounts `pages`, wires the keys and the pod connection); `styles.css`: global styles and the themes' variables.
- [`app/`](app/): `base.ts` (the path the page runs under: empty on its own pod, `/_lan/pod/<key>` or `/pod/<key>` through a gateway), `router.ts` (`/`, `/project/:id`, `/project/:id/desk/:n`, detached panels), `home.ts` (one home page for the whole account)
- [`pod/`](pod/): The pod as seen by the page: `rpc.ts` (requests, pushed events, reconnection), `transport.ts` (WebSocket, or a MessagePort to the bootstrap page through a tunnel), `remote.ts` (requests to another pod from the home page), `power.ts`, `update.ts` (signed requests), `push.ts`
- [`state/`](state/): Open project and session (tabs, split tree, tool zones), settings, git state, folder marks, the phone layout (`mobile.ts`: the view shown, the keyboard, no zoom)
- [`pages/`](pages/): `Home`, `ProjectPage` (menu bar, rails, side panels, editor area, bottom tools), `ProjectBar`, `DeskSwitcher`, `Detached` (one panel alone)
- [`ui/`](ui/): Editor area, rendered Markdown files (`MarkdownView.tsx`) and the embedded docs (`DocView.tsx`, see `help/`), status bar, diff view, overlays (modal, prompt, pick list, context menu), toasts, icons, empty states, keyboard navigation of the toolbars and tab bars (`roving.ts`)
- [`editor/`](editor/): `Doc` (buffer, revisions, undo, file format, indentation), `EditorView` (block rendering, Custom Highlight API, indentation guides, whitespace overlay, multiple carets, folding, soft wrap with rows read from the layout), `carets.ts` (words, occurrences), `folding.ts` (fold ranges), `indent.ts` (indentation detection), tokenizer and grammars, three-way merge, line diff, sub-word moves, find bar
- [`keys/`](keys/): Binding table and QWERTY / AZERTY presets
- [`lsp/`](lsp/): Client, completion, edits, rename and formatting
- [`help/`](help/): `topics.json`, the map from a place of the interface to a page of the docs (`path#heading`, checked by `make test`), and the `HelpLink` (**?**); the pages open in `doc` tabs (`ui/DocView.tsx`, `state/docs.ts`)
- [`popups/`](popups/): Search Everywhere, Recent Files and the switcher, paste from history (the history itself: `ui/clipboard.ts`)
- [`panels/`](panels/): Explorer (with file type icons, `fileIcons.tsx`), global search, Git tool (`panels/git/`: tabs, tree of changes, graph lanes `graph.ts`, commit detail)
- [`tools/`](tools/): Right-panel tools (conflicts, structure…), `InfoTool`, and `tools.tsx`, the registry of the tools of the window
- [`db/`](db/): Database explorer tool: connections, tables, SQL console, result grid (`api.ts`)
- [`console/`](console/): Console tool: terminals and tasks (`consoles.tsx`, one xterm per tab), project commands (`commands.ts`, `CommandsModal.tsx`)
- [`conflict/`](conflict/): Three-pane conflict dialog
- [`settings/`](settings/): Settings modal and its pages: `Members`, `Lan`, `Pods`, `Sync`, `Notifications` (remote access and account), themes (`themes.ts`)
- [`llm/`](llm/): AI assistant: state, agent loop, tools, prompt, Markdown, the editor of the message box shared with the Agents tool (`MessageBox.tsx`), attachments, speech recognition, sub-agents (`SubAgents.tsx`), doodles (`llm/doodle/`: document model, SVG rendering, export, modal), the board of a conversation (`llm/board/`), app previews (`previews.ts`, `PreviewCard.tsx`)
- [`agents/`](agents/): Agents tool: sessions of the worktrees of the window and their threads (`state.ts`, events dropped when older than the thread loaded, by `seq`), tabs, launcher, thread, permission cards (`AgentsTool.tsx`), the follow mode (`state.ts`: an edit tool call completed opens its file when `follow` is set on the session, `agents.follow`), the message box of a session (`Composer.tsx`: `llm/MessageBox.tsx` with the agent's commands, attachments prepared by `llm/attachments.ts` sent as parts, drafts per session)
- [`questions/`](questions/): Questions tool: the list (`state.ts`, asked again on `questions.changed`), the modal one question at a time and its auto-open (not for a question whose thread is on screen: `showsQuestions`), the forms of `elicitation/create` (`Form.tsx`, also in the Agents thread)
- [`kanban/`](kanban/): Board, ticket view, workflow actions, linked conversations
- [`docker/`](docker/): Docker tool: lists and polling (`state.ts`), detail pane, logs with ANSI colors (`ansi.ts`), disk usage, port forwards (and their list on the home page), app previews
- [`i18n/`](i18n/): `t()` and the catalogs (English source strings, French translation)

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

`cd web && npm run check` (tsc and the French catalog); browser suites ([../../e2e/README.md](../../e2e/README.md)): `editing`, `editor`, `features`, `explorer`, `popups`, `keyboard`, `mousenav`, `mobile`, `desks`, `llm`, `kanban`, `perf` and the others by tool.
