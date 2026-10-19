# Design specification

This document describes what Web IDE is meant to do and the design decisions behind it. For how the code is organised, see [architecture.md](architecture.md); for the kanban, see [kanban.md](kanban.md).

## 1. Overview

An IDE that runs in the browser, usable without installing anything on the front-end side thanks to a small local agent, the **pod**, which talks to the web page over a WebSocket.

- A browser alone cannot open a TCP socket or an SSH connection.
- The pod is a single binary (Go, no runtime dependency) running on the user's machine.
- The pod gives access to the local disk, to SSH (with the local key set), to databases, terminals and language servers.
- Updating or restarting the pod keeps the local terminals running (they run in the keeper unit): the page reconnects and shows their output without gap. A conversation of the assistant goes on too: the answer being written ends whole, a command running ends and its result reaches the model, an approval waiting is asked again. The terminals of SSH projects survive too (the keeper has its own connection), and so does an update of the keeper itself (it re-executes in place; only the SSH terminals are closed).
- Private keys never leave the machine and are never sent to a remote backend.

## 2. Stack

- TypeScript across the whole front end.
- **SolidJS** for the IDE chrome (menus, tabs, panels, settings, status, pop-ups): fine-grained reactivity with signals, no virtual DOM.
- **Editor core** (buffer, cursor, selection, highlighting): plain TypeScript without a framework, manipulating the DOM directly to avoid diffing on every keystroke.
- All persistent state lives on the pod side in `~/.web-ide`: settings (with snapshot history), project registry, per-project desk state. The browser keeps nothing essential.
- No WebAssembly to start with: plain JS first, profile later. Candidates if a bottleneck appears: the three-way merge and multi-file search.

## 3. Pod (local agent)

- WebSocket connection on a fixed, documented port (`127.0.0.1:4433` by default).
- Access to local and remote (SSH/SFTP) files: read, write, watch.
- Streams files to the front end and pushes new versions when a file changes on disk (for example when an AI agent edits it).
- Holds the desk state of each project (section 9).
- Hosts database connections (section 11) and language servers (section 12).
- Generic file reading by absolute path, not limited to the project root (needed for dependency sources and stubs, section 12).

Technical points:

- **Mixed content**: the front end is embedded in the pod binary and served from the same origin, so there is no HTTPS page talking to `ws://localhost`.
- **Pairing**: without a check, any website open in the browser could talk to the pod. The pod prints a token once; opening the URL with `?token=…` stores it in an HTTP-only cookie.
- **Persistence**: a systemd user unit (`make service`) or a one-off run (`make run`). No tray icon.
- **Installable app** (PWA): a manifest and its icons (`web/public`) let Chromium browsers install the IDE in its own window (*Install* in the address bar), where shortcuts such as `Ctrl+Tab`, `Ctrl+W` or `Ctrl+N` reach the IDE. The browser fetches the manifest without cookies: it and its icons are served before the pairing check. Installing needs a secure context: `localhost`, or HTTPS (relay, Tailscale); not plain HTTP with `-allow-remote`.

## 4. Browser file access (fallback notes)

- File System Access API (`showDirectoryPicker()`, `showOpenFilePicker()`): Chromium only, write permission asked per session.
- Drag and drop: `DataTransferItem.getAsFileSystemHandle()` in Chromium.
- `<input type="file" webkitdirectory>`: everywhere, read-only, not persistent.
- OPFS: sandboxed storage invisible to the user, only useful as a cache.
- None of these APIs gives a raw network socket.

To treat local and SSH projects the same way, the pod exposes one interface (`list`, `read`, `write`, `watch`) for both.

## 5. Layout

```
+--------------------------------------------------+
| Menu bar (project, branch)  pod status, settings |
+----+-----------+-----------------------+---+-----+
| ic | Explorer  |  Tabs                 | T | ic  |
| on |           |  Editor               | o | on  |
| s  |           |                       | o | s   |
|    |           |                       | l |     |
|    +-----------+-----------+-----------+---+     |
|    | Console (tabs)        | Problems      |     |
+----+-----------------------+---------------+-----+
| Status bar 1 2 3 +   1:1 LF …                    |
+--------------------------------------------------+
```

- **Menu bar**, from left to right: home button; project icon (click: icon editor), project title and the **worktree selector** (branch of the worktree shown, or *Ticket #<n>* for the worktree of a ticket); the File, Edit, Navigate… menus; then on the right conflicts, pod status (connection state, download and upload rate in bytes/s over a sliding window of about one second) and settings.
- **Menus**: File (new file or folder beside the active file, save, go to file, close the tab, the other tabs or all of them, resolve the conflict, settings), Edit, Navigate, Code, View and Tools (one entry per tool, with its shortcut), their entries grouped by separators. `F10` opens the first menu; in a menu the arrows move (left and right open the menu beside), `Escape` closes it and the focus goes back where it was; an entry runs where the focus was. Context menus take the keyboard the same way.
- **Context menu of the editor**: go to declaration or usages, find usages, implementations, rename, reformat; cut, copy, paste; comment, duplicate the line; show the file in the explorer, copy its relative path.
- **Search Everywhere** (double `Shift`, *Navigate › Search everywhere*): one popup with tabs, *All* (the first files, symbols and actions, each in its section), *Files*, *Symbols* (language servers, two characters at least), *Actions* (with their shortcut, the saved commands of the project first) and *Text* (lines of the project, the matches highlighted). `Tab` / `Shift+Tab` change the tab and keep the query; *Go to file* (`Ctrl+Shift+N`), *Go to symbol* (`Ctrl+Alt+Shift+N`) and the command palette (`Ctrl+Shift+A`) open it on their tab. The last query comes back selected; `Escape` gives the focus back where it was.
- **Recent Files** (`Ctrl+E`, *Navigate › Recent files*): the tools on the left (in the order of the rails, with their shortcut), the files last shown on the right (50, shared by the desks of the project, colored by their git state, a dot when unsaved) and the path of the selected one below. The previous file is selected, so that `Enter` goes back to it; the arrows move, left and right change the column, typing filters both, `Delete` forgets a file. `Ctrl+E` again keeps the changed files only (unsaved, or changed for git). With no query, the *Files* tab of Search Everywhere lists the recent files first.
- **Switcher** (`Ctrl+Tab`, often kept by the browser outside an application window): the same popup while the modifier is held; the shortcut again moves down, `Shift+Tab` up, releasing the modifier opens the selection.
- **Paste from history** (`Ctrl+Shift+V`, *Edit* menu, context menu of the editor; not in a terminal, which keeps its own paste): the texts copied or cut in the IDE (editor, terminals, fields, copy buttons and menu entries), the most recent first, shared by every window and project and kept by the pod (50 by default, settings: *Editor › Clipboard history*); copies made outside the browser are not seen. Each entry shows its first line, its number of lines and its time, the selected one a preview. `Enter` pastes where the focus was (multiple carets included) and moves the text to the top; `Shift+arrows` (or `Ctrl`/`Shift+click`) select several entries, pasted one per line; typing filters; `Delete` forgets the selection.
- **Worktree selector**: lists the main folder, the worktrees of the tickets (with their status) and the other worktrees of the repository; choosing one shows it in the same window, without reloading. The explorer, the search, Search Everywhere, the Git tool and the new consoles follow the worktree shown; the tabs and consoles already open stay, each in its own worktree (its saves, language server, tests, diff and blame), with a **chip** naming that worktree (`#<n>` for a ticket, with its title as tooltip, else the branch; a colour per worktree; none for the main folder), also in Recent Files and the switcher. A reload brings back the worktree shown and the tabs and consoles of every worktree. *Open in a new window…* opens a worktree in its own window, as before (`/project/<id>` of the worktree, which links of Claude Code still use). *Open a branch…* checks an existing or new branch out in its own worktree (`.ide/worktrees/b-<branch>`) and shows it: the main folder is never switched, so its uncommitted changes never get in the way. The setup commands of the project (`npm install`…, the project commands flagged `setup`) run in a console of the new worktree. *Remove a worktree…* deletes one of these worktrees and its project (confirmation when it has uncommitted changes); the branch is kept, its tabs and consoles close (a confirmation for unsaved changes), and a window showing it goes back to the main folder. Ticket worktrees go with their ticket, the same way.
- **Editor window**: explorer panel on the left, central editor with a tab bar, tools panel on the right.
- **Status bar** at the bottom of the window, full width, below the bottom strip and the icon rails (one bar per window, about the active pane of the desk shown): the **desks** of the project on the left (section 9), then on the right the caret position and selected characters (a click opens *Go to line*), then for a file its line separator (`LF`/`CRLF`), encoding and indentation, and the language. A click on the line separator or the encoding converts the file; a click on the indentation chooses tabs or 2, 4 or 8 spaces for this file (section 6.7). The bar is about **what has the focus**, whose items the plugins give: a file (the items above, from Code), a ticket view (its status, priority and size, each changed through its menu: the priority and the size until the development starts), a tool in a zone or as a tab; when the focus gives none, the bar keeps its row. On a phone it stays under the editor pane, hidden with it when a tool view is shown, and the desks stay in the bar of the window.
- **Icon rails** on both sides, from the menu bar down to the status bar, each with a top and a bottom group: four tool zones (top left, bottom left, bottom right, top right). Each icon toggles its tool; clicking the active icon hides it; each zone shows one tool at a time.
  - Top left: file explorer, global search, Git, kanban.
  - Top right: Assistant, database explorer, structure, conflicts, infos. Infos stacks three sections: properties (project, target, pod, active tab), connections (local SSH keys, hosts of `~/.ssh/config`) and extensions (language servers).
  - Bottom left: Console, the consoles as tabs (terminals, build output, commands).
- **Project commands** (▶ of the Console): saved commands of the project, kept in `.ide/commands.json` of the main project (local, shared by its worktrees). Each has a name, an optional description, a multiline bash command (run with `bash -lc`, edited in the editor of the IDE with shell highlighting), a flag (`serve`, `build`, `test`, `lint`, `setup`, `other`; an icon each), an optional folder relative to the root and, for `serve`, an optional port. The ▶ menu lists them (● when running, the description as tooltip), then *Run a command…* (ad hoc, as before), *Save “…” as a command…* when the active console is an ad hoc task, and *Edit commands…*: a modal with the list (filter, new, duplicate, order by `Alt+↑/↓` or drag, the order of the menu) and the detail of the command chosen (*Run*, *Delete*; edits kept until *Save*, closing with unsaved changes asks). A command runs as a task in the worktree shown, its console tagged with it: one console per command, running it again while it runs offers *Restart*, an ended one runs again in its tab, which keeps its place. Nothing starts on its own. A `serve` command with a port also gets *Open “…” in a preview* in the ▶ menu and *Start and open the app* in the modal (see App previews). The IDE relies on two flags: `setup` (the setup of a new worktree, see [kanban.md](kanban.md#git)) and `serve` (previews). The assistant and Claude Code see them too (`list_commands`, `run_saved_command`, same rule: a command running is answered as such, with its console and the end of its output, unless `restart`). With no console open, the Console lists the commands (arrows, `Enter` runs one), with *New terminal* and *Edit commands…* (*Create a command…* when there is none). Search Everywhere has an action *Run: <name>* per command (its flag icon, its description; first among the actions), which runs it (*Restart* asked when it runs); each can get a shortcut in the keymap settings (section 7).
  - Bottom right: Problems, with two tabs: the diagnostics of the language servers and their output. A dot on its icon signals errors. Docker (section 14).
- **Moving tools**: an icon is dragged to another group of the rails (same zone to reorder it); a shown tool stays shown in its new zone. The placement is saved in the session; *View › Reset the tool layout* restores the default one.
- **Tabs**: file, console and Problems tabs share one look. A new editor tab opens first, on the left of its bar (scrolled into sight); a tab already open keeps its place. The active editor tab has an inner border, 3 px at the bottom and 1 px elsewhere: accent in the focused pane, white in the active one, faint in the others. File and console tabs are reordered by drag and drop (a marker shows where the tab lands; file tabs also move to another pane); the order of the console tabs is saved in the session.
- **Focus**: the part of the window last clicked or typed in (a tool zone or an editor pane) has the focus; menus, the palette and dialogs leave it unchanged. Its active tab and its rail icon are in the accent color; the active tabs and icons of the other open tools are white (the text color in a light theme). The accent color is the one of the theme or one picked in the settings.
- **Focus outline** (settings, *View › Focus outline*, checked in the menu while on): an accent outline drawn over the edges of the focused part, 1, 2 or 3 px wide (settings).
- **Dimming** (settings, *View › Dim out of focus*, checked in the menu while on): the parts out of focus fade toward the background, by 10 to 70 % (settings, 35 % by default). Independent of the visual focus mode, which it complements.
- **Selection**: the selected item of a list, a tree or a menu (explorer, palette, completion, settings, history…) has a lighter background and an accent bar on its left, drawn a little inside so that the focus outline never hides it. No text of the interface is smaller than 12 px.
- **Visual focus mode** (settings, *View › Visual focus mode*, checked in the menu while on): everything but the focused part and the menu bar is monochrome, rail icons included: the backgrounds of the theme stay, the other colors (text, borders, highlighting) become tones between the background and the text color of the theme; images and terminal text are grayed.
- **File explorer**: a click selects a row, a double click (or `Enter`) opens the file and gives the editor the focus, or opens/closes a folder; the arrows move in the tree. Recognized file types have an icon (a colored badge for languages, a colored line icon for data, images, archives, locks, Docker, git files…). Git colors, as JetBrains IDEs: untracked red, added green, modified blue, conflict dark red, left out by `.gitignore` olive (`git check-ignore`, inherited by their content); a folder takes the color of its changes. Toolbar: locate the active file, expand all (except ignored, excluded, `.git` and `node_modules` folders, 300 folders at most), collapse all, and an *Options* menu: new file or folder, show hidden files, show excluded folders, open files with a single click, always select the opened file, refresh all (options saved in the session).
- **Folder marks** (context menu of a folder): *Source folder* (blue), *Test folder* (green), *Excluded folder* (orange, its content dimmed). Kept in `.ide/folders.json` (local, shared by the worktrees of the project). Excluded folders are left out of the global search, *Go to file* and the file tools of the assistant.
- **Bottom strip**: the tools of the two bottom zones, under the side panels and the editor. One shared height; side by side, the border between them can be dragged; alone, a tool takes the whole width.
- **Split view**: recursive pane tree (split right, split down). A file open in several panes shares one buffer, never two copies.

### Phone layout

On a screen 720 px wide or less (phones; tablets keep the layout above):
- **One bar of three rows**: home, project, branch, pod state and settings; the menus (*File* … *Tools*), scrolled sideways when they do not fit; one row of icons, the **editor** first then every tool in the order of the rails, scrolled sideways, the view shown highlighted.
- **One view at a time, full screen**: the editor or one tool, no rails nor resizers. A tool shown by any way (its icon, a shortcut, the *Tools* menu, the assistant) comes to the front; opening a file (explorer, where one tap opens it, search, go to definition, links) or touching the editor icon brings the editor back; closing the tool shown too.
- **No zoom of the page**: the viewport is fixed (no pinch, no double tap, no zoom on focus), everywhere in the window — the overlays mounted outside the app (menus, modals, popups) included; the doodles and the board keep their own pinch. The text of any field takes 16 px at least on a phone (iOS zooms a focused field below that), and a zoom the system imposes anyway is undone by re-applying the viewport.
- **Editing on a double tap**: the editor is locked (scrolling and selecting with a long press work, the keyboard stays closed); a tap shows *Double-tap to edit*, a double tap unlocks it with the caret under the finger and the keyboard open. A padlock floating at the top right of the file locks it again, as does showing another view or file.
- **Keyboard**: no field takes the focus by itself (the search field, the message box of the assistant, a terminal): the keyboard opens when the user touches it; the icon of a tool focuses the tool itself. The page takes the height left by the on-screen keyboard and the line of the caret is scrolled back into view.

## 6. Editor

### 6.1 Syntax highlighting

- Language detected by extension first, then by content (shebang, `<?php`, `package main`, `server {` blocks).
- At least PHP, JavaScript, TypeScript, Python, Go and nginx configuration.
- One grammar per module: an ordered list of `{token type, regex}` rules; the first rule matching at the current position wins. Adding a language does not touch the engine.
- Rendered with the **CSS Custom Highlight API** (`CSS.highlights`, `::highlight()`): one `Highlight` per token type, no `<span>` in the DOM, no caret jumps.
- API limits: only `color`, `background-color`, `text-decoration`, `text-shadow`, `font-style`, `caret-color` and `text-emphasis-color` apply. No `font-weight` (so no bold); italics are possible.
- Re-tokenising invalidates the `Range`s: tokenising is incremental and the editor renders in blocks for large files.

### 6.2 Find in the open file

- Floating bar at the top right of the editor, toggled by `F3`.
- Query field and three options: `Aa` (match case), `ab|` (whole word), `.*` (regular expression).
- `n / N` counter, previous/next buttons wrapping around, close button.
- Text mode escapes the query; regex mode uses it as is. An invalid pattern shows an error instead of the counter.
- Rendered with the same API: a `search-match` highlight for all matches and a higher-priority `search-current` for the current one.

### 6.3 Navigation shortcuts

- `Ctrl+Arrow`: native browser behaviour, not intercepted (with several carets, the editor moves them all by word).
- `Alt+Arrow`: jump between case boundaries inside a word (`foo|Bar`, `XML|Http|Request`, around `_` and `-`, between letters and digits). `Shift+Alt+Arrow` extends the selection.
- `Ctrl+Alt+R`: opens the conflict resolution dialog of the current file (no effect without a conflict).

### 6.4 Display

- The text starts right against the gutter, without a left margin.
- **Indentation guides** (setting, on by default): a thin vertical line at each indentation level, the step being the most frequent indentation increase of the file (else the tab size); blank lines take the smaller indentation of the lines around them. The guide of the block holding the caret (or opened by the caret line) is brighter.
- **Whitespace** (setting and *View › Show whitespace*, off by default): a dot for each space, an arrow for each tab, `↵` at each line end, drawn faintly over the text by an overlay copying the visible lines in transparent characters (exact widths, tab stops included).
- **Word wrap** (setting: Markdown and text files by default, every file, or never; *View › Wrap the long lines* or Alt+Z toggles it for the open document until the page reloads): long lines wrap at the width of the editor, at word boundaries, without horizontal scroll. The gutter shows the number of a line on its first row; the current line, the change marks, the carets and the guides follow the rows; Up/Down and Home/End move by row (Home on the first row keeps the smart Home).
- **Markdown rendering**: a `.md` file opens rendered (headings, tables, task lists, code colored by the editor grammars, Mermaid diagrams), from the text of the buffer, unsaved edits included. A floating button at the top right, *View › Markdown: rendered / raw* or Alt+M switches to the source and back, at about the same place of the file; a double-click on a block shows the source with the caret on its line; Ctrl+F and a jump to a line (search, Go to line, `/open` link) show the source. Relative links open the files of the project (`#L12` at that line), `#anchors` scroll to their heading, other links open in the browser; images of the project are read through the pod (`fs.image`, 10 MB at most). The choice is kept per tab until the page reloads.

### 6.5 Folding

- Ranges: brackets outside strings and comments for the C-like languages (the line of the closing bracket is hidden too and its text follows the placeholder: `func main() {⋯}`), the indentation for Python, YAML, HTML and plain text, the headings for Markdown (outside code fences).
- A chevron in the gutter (shown on hover, always for a folded range) folds or unfolds; a folded range shows a `⋯` placeholder after its header, a click on it unfolds.
- `Ctrl+-` / `Ctrl+=` fold and unfold the block of the caret, `Ctrl+Shift+-` / `Ctrl+Shift+=` every block (numeric keypad too; on AZERTY, `-` is the key of the `6`). Also in the *Code* menu.
- Hidden lines stay in the DOM, in blocks set to `display: none`: offsets, copy and search are unchanged.
- An edit inside a range, or adding or removing lines at its header, unfolds it; moving the caret to a hidden line (go to line, search, navigation) unfolds it. Typing on the header line keeps the fold.
- Folded ranges are kept in the session with the cursor of the file.

### 6.6 Multiple carets

- The primary caret is the browser selection; the other selections are drawn by the editor (a `Highlight` for their text) and follow the edits made elsewhere. With several carets, the editor draws them all, the primary one included (the browser shows no caret at the end of a selection).
- `Alt+J` selects the word at the caret, then adds the next occurrence of the selected text (whole words when started from the word at the caret), which becomes the primary caret; `Shift+Alt+J` removes the last one added; `Ctrl+Alt+Shift+J` selects every occurrence. The first and the last are also in the *Edit* menu.
- Column selection: drag with the middle button, or with `Alt+Shift` and the left button. Lines too short for a caret at the column of the mouse are left out. The paste of the primary selection that Linux does when the middle button is released is dropped.
- `Alt+click` adds a caret or removes the one clicked. `Escape`, a click or a move made by the browser (`Ctrl+Home`, select all, page keys) leaves a single caret.
- Typing, `Backspace`/`Delete` (also by word or line), `Enter` (auto-indent), `Tab`, the arrows (`Ctrl` by word, `Alt` by sub-word), `Home`/`End` and their `Shift` variants act on every caret; each such edit is one undo step, and undo leaves a single caret. A dead key or an input method types at every caret too. Copy joins the selections one per line (the lines of the carets when nothing is selected); a paste with as many lines as there are carets gives one line to each.

### 6.7 File format and indentation

- **Encoding**: the pod decodes UTF-8, UTF-8 with BOM, UTF-16 (with BOM) and, for other non-binary data, Windows-1252; anything else opens as binary. The editor always works on UTF-8 text.
- **Line separator**: CRLF line ends become LF in the editor. A file is written back in its encoding and with its line separator (the most frequent one when they were mixed). A character the encoding cannot hold makes the save fail with a message, and the file keeps its format.
- **Indentation** of each file is detected (tabs, or the most frequent increase of spaces): `Tab`, auto-indent and the guides follow it; files without indentation follow the settings.
- The status bar (section 5) shows the three and changes them: converting the line separator or the encoding saves the file at once (with its unsaved changes); choosing an indentation only changes what the editor inserts from then on, and is kept in the session.

## 7. Keyboard shortcuts

- **Centralised** binding table: an action (`search.find`, `conflict.resolve`, `view.splitRight`…) maps to a key combination stored by physical `code`, not by `key`.
- Default **QWERTY** and **AZERTY** presets (two data files).
- Layout detection with `navigator.keyboard.getLayoutMap()` when available (Chromium), otherwise a manual setting.
- User changes are stored as overrides (`action → binding`) on top of the preset, and can be reset per action.
- Assigning a combination checks for collisions and asks before overwriting.
- Menus and the command palette show remapped shortcuts without a restart.
- **One shortcut per tool** (*Tools* category): it shows the tool and gives it the keyboard (its terminal, its prompt, the selected row of its tree, else its first field or button); pressed again while the tool has the focus, it hides it and the editor gets the focus back. Defaults, in the order of the rails: `Alt+1` Explorer, `Alt+2` Search, `Alt+3` Git, `Alt+4` Kanban, `Alt+5` assistant, `Alt+6` Database explorer, `Alt+7` Structure, `Alt+8` Problems, `Alt+9` Docker, `Alt+0` Infos, `Alt+F12` Console; Conflicts has none. *Show / hide the left panel, the tools, the consoles* keep their menu entries without a default shortcut.
- **Project commands** (*Project commands* category, listed while a project is open): one action *Run: <name>* per saved command, without default shortcut, also from a terminal. It is bound by the id of the command (`command.run.<id>`), so renaming the command keeps its shortcut.
- **Escape** in a tool gives the focus back to the editor, unless the tool used the key (closing a popup, stopping an answer, leaving an edit) or the focus is in a terminal.
- **Toolbars and tab bars** (rails, panel headers, toolbars, editor and tool tabs, settings sections) are one Tab stop each: the arrows move between their buttons and tabs (up and down in the rails), `Home` / `End` go to the ends, `Enter` or `Space` activates a tab, `Delete` closes it. The Tab stop is the item focused last, else the active one. A click on an editor tab gives the keyboard to the editor.

## 8. Synchronisation and conflicts (files changed by other tools)

- The editor works on an in-memory copy of each file, never directly on the pod stream.
- Three versions per open file:
  - `base`: content when loaded;
  - `local`: current buffer, with unsaved changes;
  - `remote`: new version pushed by the pod (typically after an AI agent changed it), with a revision number.
- On each `remote`: line-based **three-way merge (diff3)**.
- **Clean merge**: applied silently, `base` becomes `remote`, discreet toast.
- **Conflict**: the file enters a conflict state; the local buffer is not overwritten until the user decides.
- Conflict UI: warning banner on the tab, a **Conflicts** tool listing conflicted files, and a three-pane resolution dialog (local changes, editable result, new version) with per-block accept buttons.

## 9. Desk state and persistence

**Pod side, per project**: open files, per file the cursor position, folded ranges and chosen indentation, explorer options, consoles, split layout (tree, sizes, active file per pane). Pushed over the WebSocket on every change (debounced for the cursor), restored on load or reconnection, so the desk is the same from another browser or machine.

**Desks** ([desks.md](desks.md)): a project has numbered desks, as the virtual desktops of GNOME or KDE, each with a state of its own (layout, tabs, tool zones, panel sizes). The documents and their unsaved changes stay shared: a file open on two desks is one buffer. The Recent Files are shared by the desks of the project.

- `1 2 3 +` on the left of the status bar (a phone: in its menu bar). A desk is a link: a click switches the window without reloading it, a middle click opens the desk in a new tab; its menu opens it in a new tab, renames it or removes it (not desk 1: its tabs close, the files stay open elsewhere). A window on a removed desk goes back to desk 1.
- The URL names the desk: `/project/:id` is desk 1, `/project/:id/desk/N` the others; the history goes back to the previous desk. The home page opens the desk last shown on the device.
- Two windows on the same desk mirror each other; two windows on two desks do not touch each other.
- `Ctrl+Alt+1…9` goes to desk N, `Ctrl+Alt+Shift+1…9` sends the active tab there; the tab menu has *Move to desk N* and *Move to a new desk*; a tab dropped on a desk number moves there. AltGr typing a character (Ctrl+Alt on Windows) stays text.
- **Tiles**: in a desk of tiles (Grid or Scroll) a tile is either a **group of files** (files, diffs, SQL consoles, tables, tickets, with their tabs) or **one tool** alone (a tool, a console, the kanban board), never a mix. A file opens in the group of files focused last, or makes a group beside the focused tile when there is none; a tool shows in its own tile; a pane mixing both is split, each tool beside. *New terminal* opens a terminal alone in a new tile; the Console tool in a tile is the whole tool, with its tabs (a console shown in its own tile offers there to bring it back with a click: one terminal per window). In Full mode tools stay in their zones (*Open in a window* only): the tiles of tools of a desk switched to Full are hidden, kept for the way back, and their tools show in their zones (the first tile of a zone is the tool shown).
- **Layout of a desk** (its menu): *Full layout* (rails and zones) or *Grid of tiles*, like i3: no rails nor zones, the split tree of the editor is the whole window and each pane is a tile (a file, a tool, a console; several tabs make a tabbed tile). A tool or a console alone in its tile has no tab bar: its corner has *Open in a window* and *Close* (whose menu is the tab menu), discreet over a terminal until hovered. *Open in a window* is also in the menu of a tool or console tab. Switching keeps the tabs and the zones. In a Grid desk a tool shortcut shows the tool as a tile and a new terminal opens as a tile. `Ctrl+Alt+Enter` (or the tile button after `+`) opens a file, a tool, a new terminal or a console in a new tile beside the focused one: on its right, or below it after `Ctrl+Alt+V` (or the ◨ / ⬓ button; saved with the desk), the side shown by a line on the focused tile, as i3; `Ctrl+Alt+Shift+R` resizes the focused tile with the arrows by 12 px (right and down grow it, left and up shrink it, Shift: 48 px) until Escape, and *Wider / Narrower / Taller / Shorter tile* can get shortcuts; `Ctrl+Alt+Shift+arrows` move the focus between tiles; *Swap with the tile on the left / right / above / below* (no default shortcut); closing the last tab of a tile closes it, `Alt+W` on an empty tile too.
- *Scrolling columns*, like niri or PaperWM: the same tiles in columns side by side, in a strip wider than the window, 3 columns in sight above 1280 px, 2 above 720 px, 1 below, following the window width live. A new tile opens a column right of the focused one and scrolls it into view (after `Ctrl+Alt+V` it stacks under the focused tile instead); the focus moves with `Ctrl+Alt+Shift+arrows` and the view follows. `Ctrl+Alt+Shift+PageUp / PageDown` move the column, `Ctrl+Alt+Shift+F` gives it the next width (1/3, 1/2, 2/3, the whole view, never less than one column in sight), as does dragging its right edge or the resize mode; `Ctrl+Alt+Shift+S` stacks the tile in the column on its left, `Ctrl+Alt+Shift+U` takes it out to a column of its own. Shift+wheel, the touchpad and the marks of the strip under the columns scroll; a mark focuses its column. The columns are the panes of the Grid tree (a row split at the root), so switching between Grid and Scroll loses nothing.

**Settings** (`~/.web-ide/settings.json`): themes, fonts, shortcuts, custom highlighting rules, language.

- Full snapshot history on each significant change; entries are never modified.
- A `current` pointer to the active entry; a rollback creates a new entry copying the old one, so a rollback can be undone.
- Bounded number of entries (oldest purged); forced snapshot before importing highlighting rules.
- Every browser connected to the same pod shares the settings.

## 10. Settings, projects and detached windows

### Settings

Large modal with navigation on the left: themes (with the accent color and the visual focus mode), fonts, editor (tab size, word wrap, indentation with spaces, current line, indentation guides, whitespace), keyboard shortcuts, syntax highlighting (add, edit, export rules as JSON per language), language of the interface (English, French or Spanish, or the language of the browser).

### Help

The docs are embedded in the pod (`docs/`, the glossary and the READMEs of the code), so they match the running version and work offline. The *Help* menu (and the palette) has *Documentation* (the index), *User guide*, *Glossary* and *Keyboard shortcuts* (the settings). A page opens read only in a *doc* tab, the same tab for every page: its title is the first heading of the page, relative links to other pages open in it (*Back* returns, *Documentation* goes to the index), a `#heading` link scrolls, pictures show, Mermaid diagrams are drawn, links to `https:` open in the browser and a link to something that is not a page of the docs says so. The tab is restored with the desk.

A **?** next to a place of the interface opens the page that explains it, at its heading: the settings sections (Language, Projects folder and pod, Remote access, with the relay and the local network, Notifications), the kanban toolbar and the Agents tool. The map from place to `path#heading` is `web/src/help/topics.json`; `make test` checks that each page and heading exists. The **?** shows in the window of a project only (the page opens there).

### Home page and projects

- **One home page for the account**: one flat list of the projects of every pod — this pod's and those the other pods publish — the last opened first, each with the badge of its pod (when the account has several) and its icon. A project of another pod opens in the same window, with the front end of that pod; it is greyed and not openable when its pod is offline or speaks another multi-pod protocol (the reason in its tooltip). Its icon, its form (*Edit*) and *Remove from the list* work as for a project of this pod: the request goes to the pod that holds it, which checks and saves it. *New project* picks the pod (this one by default): the folder, the folder browser, the workspace and the SSH hosts are those of that pod, and the new project opens with its front end. Below the list, one line per other pod: online or not, gateway, version (*Update this pod*), *waiting for the sync key*, *LAN one-way*. A pod that sleeps offers **Wake** (Wake-on-LAN, sent by an awake pod of its network); one that runs, this one included, a power menu: *Restart the pod*, *Reboot*, *Shut down*, the last two only once `sudo web-ide-pod allow-shutdown` ran on its machine (relay.md, *Power*).
- The home page is always the one of the pod the window entered through (`127.0.0.1` or the LAN port: that pod; the relay origin: the entry pod of the bootstrap page, moved by a failover): the home button of a project of another pod goes back there, and the home page of a pod served under a prefix (`/_lan/pod/<key>/`, `/pod/<key>/`) redirects to it.
- A project is **local** or **SSH**, with an optional title and description (name derived from the path or the host otherwise).
- The project registry lives in the pod. Each project has its own URL, `/project/:id`, with its own desk state on the pod (one per desk, `/project/:id/desk/N`).
- Every project is a git repository: creating a project runs `git init` (first branch `main`, an empty first commit when the folder is empty) unless the folder is already in a repository, and adds the optional remote as `origin`. An SSH host not reachable at creation (password) gets it at the first opening (`gitSetup` pending in the registry).
- **Project icon**: a glyph (a subset of Lucide) or 1 to 3 characters on a shape (circle, rounded square, square, hexagon, diamond) filled with a colour or a two-colour gradient in one of 8 directions. Shown on the home page, and as the favicon of every window of the project so that browser tabs tell the projects apart; the worktree of a ticket shows the icon of its project with a dot. The page draws the SVG and saves it in `.ide/icon.svg` with its description in `.ide/icon.json`; a project without one gets one generated (initials, colour and shape from its id).
- Each project has a `.ide` folder: project settings, folder marks, database connections (without secrets), prompts, icon, conversations of the assistant and the kanban. The whole folder is ignored by git (`.ide/.gitignore` is `*`): the IDE never adds files to the repository. The worktrees of the project (tickets, branches) have no `.ide` of their own: they read and write the settings of the main project. When files of `.ide` were committed before, the IDE offers once, at the opening of the project, to take them out of git (`git rm --cached`, the files stay on disk, the user commits); it never does it on its own.

### Detached windows

Each panel has an id and can be opened alone:

- `/project/:id/editor`
- `/project/:id/tool/:toolId`, any tool of the four zones, the Console with all its tabs included.
- `/project/:id/console/:consoleId`, one console alone (from a console tile).

Each window is one more WebSocket client on the same pod session; the shared buffers keep them in sync.

## 11. Database explorer

Tool of the right panel for SQLite, PostgreSQL and Redis. All connectivity goes through the pod.

### Tree

- Connections > databases > tables > columns (name and type) and indexes.
- Redis has typed keys (`string`, `hash`, `list`, `set`, `zset`, `stream`) with their TTL, under each `dbN`.
- Context menus: connection (edit, duplicate, delete, refresh), table (view data, view DDL, empty query), column (copy name, copy qualified name), index (view definition).
- Double-clicking a table opens the table view. Status indicator per connection: connected, error, untested.

### Connections

- SQLite: file path (local or over SSH). PostgreSQL: host, port, database, user, password, SSL mode. Redis: host, port, database index, password.
- **Test** button in the form.
- Password either remembered by the pod or asked at each opening (kept for the session).
- Optional **SSH port forward** per connection (PostgreSQL and Redis).
- Connection settings are stored in the project's `.ide` folder; the page only receives non-sensitive metadata.

### SQL console

- Highlighting by query language (SQL, or Redis commands).
- Several statements separated by `;`; the active one (at the cursor) is outlined. Running with several statements opens a keyboard-driven picker preselected on the active one. `Ctrl+Enter` runs.
- Results in a grid with duration and affected rows; history; auto-commit on/off, commit, rollback, cancel.

### Table view

Paged content of a table without writing a query, sharing the connection of the console.

## 12. Code navigation (LSP)

Goal: PhpStorm-like navigation (declaration and usages, implementations, type declaration, super method, related symbols, tests).

- **LSP** (Language Server Protocol over JSON-RPC): the pod runs the servers, the editor talks LSP through the WebSocket.
- Servers: `gopls` (Go), `intelephense` or `phpactor` (PHP), `pyright` (Python), `typescript-language-server` (JavaScript/TypeScript).
- Requests: `definition`, `references`, `implementation`, `typeDefinition`, `documentSymbol`, `workspace/symbol`, `completion`, `rename`, `formatting`, plus `didChange` for edits. The pod guarantees that a request is sent after the `didChange` notifications received before it.
- Built outside LSP: super method, related symbols, tests (naming convention or per-project configuration).
- **One server per project and language**, with the project root as `rootUri`. The pod detects languages (`composer.json`, `package.json`, `go.mod`…), routes by file extension and stops servers a while after the last window leaves.

### Sources outside the project

Definitions may point outside the project (`lib.es5.d.ts` in `node_modules/typescript/lib`, PHP stubs, `GOROOT/src`, `.pyi` files bundled with pyright). The pod reads any reachable absolute path; such tabs are **read-only** and absent from the explorer. On an SSH project, paths are resolved on the remote host.

### Git

- Git tool: a branch bar (switch, create, ahead/behind, pull, push, fetch), then two tabs; the shown tab and the heights of the bottom areas are saved in the session.
- **Commit** tab: the changed files as a tree drawn like the explorer (git colors, file icons, chains of single folders joined on one row, `a/b`), under a *Changes* root row. Each row ends with a check box: checked when the file is staged, partial when it is staged with changes left (or, on a folder, when part of its files are); a click stages or unstages the file, the folder or everything. Checking a conflicted file marks it resolved (confirmed when it still holds conflict markers). A click opens the diff against HEAD, a double click the file; arrows move, `Space` toggles the box; context menu: differences, open, stage, unstage, discard, copy the path. At the bottom (height set by a handle): commit message, *Amend* (filled with the last message), *Commit*, *Commit and push* with a menu to push with `--force-with-lease` or `--force` (confirmed).
- **History** tab: the commits of the current branch with their graph, as `git log --graph --pretty='%h -%d %s (%an %ar - %ad)'` (lanes drawn in SVG, merged branches beside), loaded 100 at a time while scrolling; a search on message, author or hash (no graph then). A click shows the commit at the bottom (height set by a handle): actions (copy the hash, new branch at the commit, revert, reset soft / mixed / hard), author, dates, parents, full message, and the changed files as a tree; a file opens its diff against the first parent. The same actions are in the context menu of a commit.
- Pull, push and fetch run in a terminal of the bottom panel (credential prompts stay interactive). A branch without upstream is pushed with `-u origin <branch>`.
- Side-by-side diff tab (working tree ↔ index, index ↔ HEAD, working tree ↔ HEAD, or a commit ↔ its first parent); gutter markers for added, changed and removed lines while typing.
- `git` runs through the same executor as the rest, so it also works on an SSH host.

## 13. Assistant

Right-panel tool talking to a **llama.cpp** or **Ollama** server, or an **OpenAI-compatible provider** (OpenAI, OpenRouter…: address and API key, never sent back to the page; models listed by `/models` or typed by hand with their context and capabilities; only standard fields sent; rate limits retried with `Retry-After`, then the errors explained: refused key, no credit, unknown model). Each server has a number of conversations at once, and may be offered to the sub-agents with a note for the model.

- **Message box**: a Markdown editor (the code editor without gutter: highlighting, soft wrap, multiple carets, Alt+J, undo) growing up to 40 % of the height. Enter adds a line and continues the lists (`- `, `1. ` numbered on, `- [ ] `; Enter on an empty item ends the list); Ctrl+S or Ctrl+Enter sends. `/commands` and `@paths` are completed and highlighted. Bar: mode, dictation, attachments, options, then the context gauge, the model and send. Full screen (button at the top right, `Ctrl+Shift+E`): the box takes the whole tool, the thread hidden; Esc or sending leaves it, the draft, caret and undo kept. A conversation linked to a ticket shows a bar inside the frame of the box, on top: *Ticket #n* (its title in the tooltip, a click opens it), its status, and the role of the conversation on the right. Two LED strips, above and under it, show the mode (Orchestrator green, Build yellow, Briefing orange, Plan blue; violet during a compaction), also the color of the mode button: brighter while the box has the focus, pulsing while the assistant works. Placeholder: *Message…* (*Queued message…* during an answer); else *Link to a ticket…* once it has messages.
- Markdown answers with highlighted code and Mermaid diagrams, reasoning and tool calls as blocks with their duration (open and following their stream while they run, folded once done), the reasoning and tool calls joined in one block across the answers of a turn until a text or a card (the last chain of the conversation shown whole, the others folded behind a "N actions" label with their failures and the time of the whole block), a shell command shown as in the Console (prompt, output with its ANSI colors, exit code in its row), live token counters.
- A stuck answer (a reasoning that repeats a line 10 times, stopped at once, or one that ends without text nor action) is set aside and asked again once with a nudge to act; stuck again, the turn ends. A failed answer stays shown after *Resume*, marked as not sent back.
- Reasoning effort: dynamic (the most after a message of the user, medium after tool results, low after simple edits that succeeded, the most after a failure; optionally chosen by the model with `set_effort`) or fixed by the user; shown on the reasoning block.
- Statistics (assistant.stats, a view in the column of the board): by conversation with its sub-agents, or for the project filtered by model, effort and period; speeds, generation and thinking time, tools (time without the approval waits, failures by kind), calls at once, repeated calls, context by part (estimated), its curve and the compactions.
- Turn loop with tool calls: files (read, search, edit: directly in the project and in `/tmp`, elsewhere with confirmation or automatically), language servers, shell commands (run freely when they touch nothing out of the project and `/tmp`, else confirmed), IDE (open a file, focus a panel), consoles, the saved commands of the project (`list_commands`, `run_saved_command`: run freely in Build mode, the build, test and lint ones in the other modes), kanban, questions to the user (`ask_user`). It runs in the pod: a conversation goes on when its window closes, any window follows it, writes to it or stops it, and several conversations run at once (a limit per model server, the others queued).
- Instructions and skills loaded the same way as Claude Code (`AGENTS.md` first, then `CLAUDE.md`, `.claude/skills`…; a `CLAUDE.md` importing `@AGENTS.md` loads it once). The first time the assistant reads, edits or lists in a folder, the `AGENTS.md` (else `CLAUDE.md`) of that folder and of its parents up to the root is appended to the tool result, once per conversation: it is kept with the conversation, comes back in the system prompt after a compaction, and the context popover lists it. Editable system prompt, Orchestrator / Build / Plan / Briefing modes (the Orchestrator, default of a new conversation, steers the day from the kanban with action cards and opens the right conversation, see [kanban.md](kanban.md); the Briefing mode questions the user and writes kanban tickets), context compaction.
- Conversations stored per project in SQLite; an answer survives a page reload and can be followed from another window.
- Local speech recognition: dictation and audio files are transcribed in the browser by Whisper; audio never leaves the page.
- Attachments from the paperclip menu: files (images, video, audio, PDF, text) and doodles, drawings sent as an image with a text description ([doodle.md](doodle.md), `Ctrl+Shift+D`).
- **Sub-agents**: a conversation delegates a self-contained task with `spawn_agent` (title, task, files to read first, mode): a child conversation with a fresh context and the same rights (mode, ticket, options, model), running in the background; at most 5 running per conversation, and a child cannot start children (except under an Orchestrator: depth 2).
  - The child reports progress with `agent_note`, asks its parent with `agent_ask` (its turn stops until the answer; 10 questions at most) and ends with `agent_report` (summary, files changed, done or blocked). A child answering without tools is reminded once to report; its next plain answer is taken as its report.
  - The notes, questions and reports come to the parent as messages; a question or a report starts a turn of the parent (queued while it runs, kept while it waits for the user), a note is read at its next turn. The parent answers with `agent_reply` (or first asks the user with `ask_user`), writes with `agent_message`, stops a child with `agent_stop` and lists them with `agent_status`. A child stopped by the user or failing is reported to its parent.
  - A child runs on the server and model its parent gives (`server`, `model` of `spawn_agent`, among the servers offered to sub-agents, listed with their notes in the parent prompt), else on the default of the settings (*Sub-agents* tab), else on the parent's. Its tokens and cost (when the provider tells it) are summed on its card. An error of the provider ends it in error, reported to its parent.
  - What a child needs confirmed (a file change, a command) waits for the user, never for a model: a toast *Sub-agent “…” asks to change a file* with *Open*, and a system notification when the page is hidden.
  - The parent thread shows a card per child (status, latest note, open question, *Stop*, *Open*) and the events as cards; a child thread starts with its task under a header *Sub-agent of “…”* linking to its parent. The side bar lists first the last Orchestrator conversation and the older ones whose children still work, each with the tree of its working children; then the other active conversations (running, waiting for the model server or for an answer, failed, or with a working child) as trees, the most recent first; then the others by day. A child works until it reports or is stopped by hand (an error leaves it working); once ended it goes to the history of the day it ended. A child whose parent is gone stands on its own. A dot tells the state of each conversation: pulsing while it runs, orange when questions, a plan or a change wait for the user (or a question of a sub-agent for its parent), red when its last answer failed; an ended sub-agent has none. A failed conversation offers *Abandon* (in its thread beside *Resume*, and in the list): it is no longer shown as failed and goes to the history until it runs again; a sub-agent is stopped and its parent told.
  - **Adoption**: a conversation that runs on its own (a development started from a card of the Orchestrator…) is not a child. The Orchestrator takes it under its wing with `agent_adopt`: it becomes its sub-agent (header *Followed by “…”*, nested in the side bar) but keeps its tools and its user (ask_user, files), and runs where it ran (the worktree of its ticket). A development started from a card of the Orchestrator is adopted by it from its start. An adopted conversation is asked at once to announce itself with `agent_note` (what it works on, where it is), notes its milestones, and sends `agent_report` when its task is over; it is never told to report when it answers the user. An error or a stop is reported too; the user going on with it makes it work for its parent again. The Orchestrator also resumes a failed conversation (`agent_resume`, from its last completed step like *Resume*; `list_conversations` marks the failed ones), adopting it on the way when it runs on its own.

### Agents (CLI agents through ACP)

The **Agents** tool (right bar, beside the assistant) runs coding agents installed on the machine — Claude Code (through its adapter `claude-agent-acp`), opencode (`opencode acp`) and pi (`pi-acp`) — through the [Agent Client Protocol](https://agentclientprotocol.com): JSON-RPC over the standard input and output of one process per session. It will replace the assistant over time; both live side by side for now.

- **Tabs**: one per session, like the Console. A dot tells the state: starting, ready, working (pulsing), waiting for the user (orange), stopped (red). The title is the agent's name, then the title the agent gives the session.
- **+** lists the agents: an installed one opens a session in the project (or the worktree shown); a missing adapter offers *Install*, which runs `npm install` into the data folder (`<data>/acp`, versions pinned) — an adapter already in the `PATH` is used first. opencode is installed by hand (its command is copied). The empty tool shows the same list as cards. Local projects only for now.
- **Thread**: the user's prompts, the agent's messages (Markdown, streamed), its thoughts (folded), its plan, its tool calls (title, status, files, the diff of an edit), notices (stopped, restarted). A **permission** asked by the agent is a card in the thread with its options (*Yes*, *Always*, *No*…); the tab waits meanwhile. Stop cancels the turn (the open questions are cancelled with it).
- **Prompt**: the message box of the assistant (the same editor: Enter adds a line, Ctrl+S or Ctrl+Enter sends, dictation, files, images, doodles and screenshots from the paperclip or pasted). `@` completes the files of the project, `/` the commands the agent announces (its commands and skills, with their description). The prompt goes as ACP content blocks: its text, a resource link per `@path` that exists, its images (when the agent takes images), the text of its files and doodle descriptions. A prompt sent during a turn waits in a queue kept by the pod (shown above the box, taken back or removed) and goes at the end of the turn, also after a reload or a restart. ↑ / ↓ in an empty box go through the prompts sent. The bar is the one of the assistant, from the settings the agent offers (`configOptions`): the mode as its mode button (colored by the kind of mode: manual, plan, edits accepted, bypass; Shift+Tab goes to the next), the model as its model button, the other settings (effort…) in the options popover, the context used as a ring whose popover adds the work time and cost of the session and *Compact now* (sends `/compact` when the agent announces it); the full screen button hides the thread. On the phone the box is not focused until touched.
- **Follow mode**: the eye in the bar of the box (or the menu of the tab) opens in the editor the file of each edit the agent finishes (the `locations` of an `edit` tool call, else the path of its diff; opencode's relative paths are made absolute), once per tool call, in the windows showing the session. Off by default, saved with the session. The IDE does not offer the ACP file system and terminals to the agents: none of the three uses them (spike of 2026-10-13: Claude, opencode and pi edit and run commands with their own tools), so an edit shows in an open editor once written to the disk.
- **Life of a session**: the process runs in the keeper, so it survives the pod: the next pod follows it again from where it read its output (its thread and offset are saved in `<data>/agents/<project>/<id>.json`). A process that ends is started again and its session loaded (`session/load`, the replay dropped); three ends within a minute stop it with the end of its error output and a *Restart* button. Closing the tab ends the session and its process.
- **Work time and cost**: each turn is timed from the prompt to its end, the time waiting for the user (open permissions) apart, and its tokens are read per model (Claude and opencode tell them in the answer of the turn; pi in its session file). The cost is the one the agent tells, else the tokens × a price table kept in the pod (`<data>/prices.json` adds or replaces models); under a subscription it is shown as *API equivalent*, and a local or unknown model has tokens but no cost. Shown on the tab (tooltip), on the ticket (the sessions run in its worktree, a badge in its header) and for the project with its worktrees (the Agents launcher and the Infos tool, today / 7 days / all).
- **MCP of the IDE and the kanban**: every session gets the MCP server of the IDE (kanban tools, saved commands) when its agent speaks MCP over HTTP. With a development agent chosen in the kanban settings, *Dev session* opens one session per ticket in its worktree, its briefing as the first prompt; the corrections go to the same session; the ticket opens it; the worktree removed ends it ([kanban.md](kanban.md#development-by-a-cli-agent-agents-tool)).

### Questions

The **Questions** tool (right bar, a badge with their number) lists every question waiting for the user in the projects of the window, whatever asked it: a permission or a form of an Agents session (Claude's *AskUserQuestion* comes as a form, `elicitation/create`), an `ask_user` of the assistant, an `ask_user` of a CLI agent through the MCP of the IDE (Claude Code in a terminal too). The sessions of the worktrees of the project are included, a ticket's development among them.

- **Modal**: one question at a time, in front of everything, with the last message of its asker above it (folded when long); a permission shows its options, a form its fields (a choice, several choices, yes/no, a number, a text — Claude's "Other" box among them), an `ask_user` the card of the assistant. *Skip* answers a form or an MCP call without an answer, *Cancel* stops the tool of the agent, *Later* closes the modal and keeps the question in the list. The next question follows the one answered.
- **Open questions automatically** (the setting at the top of the tool, on by default): a new question opens the modal by itself, unless its thread is on screen (the session shown in the Agents tool, the conversation shown in the assistant): its card is answered there. The menu of an Agents tab overrides it for that session (as the setting, always, never). The questions already waiting when a window opens do not pop up.
- A question withdrawn by its asker (`$/cancel_request`, the turn stopped, the agent ended) leaves the list and its modal. The cards of the thread show the answer given.
- **MCP `ask_user`**: the questions of the assistant's types, plus a `context` shown above them; the call returns the answers. It waits 4 minutes at most, then answers that the questions are still open with their id: the agent calls again with `wait` to go on waiting (an MCP client gives up on a long call).
- A new question of an agent sends the push notification *The agent needs you* (#30, not to a device with a visible window); it opens the project on the first question.

## 14. Docker

Tool of the bottom right zone (beside Problems) driving Docker and Docker Compose. Everything goes through the `docker` command run by the executor, so on an SSH project Docker is the one of the SSH host. Docker missing, its daemon unreachable or not allowed (user outside the `docker` group): the tool shows the error of `docker` and a hint, never `sudo`.

Five tabs (the shown tab is saved in the session), and a detail pane beside the list of the first two:

- **Project**: the services of the Compose file at the project root (`compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`, with the override files Compose loads itself). A first *Stack* row, then one row per container (a declared service without container shows as *not created*): state dot, name, image, published ports, CPU and memory. Profiles declared by the file are chosen in a menu (saved in the session). Without Compose file, the tab says so.
- **Host**: every container of the host (`docker ps -a`), grouped by Compose project.
- **Disk**: space used by images, containers, volumes and the build cache (total, active, reclaimable), with the list of images and volumes. Prune buttons, each confirmed: stopped containers, dangling images, unused images, anonymous volumes, all unused volumes (stronger confirmation: data is lost), build cache. An unused image or volume is removed from its context menu.
- **Port forwards** (SSH projects only): see below.
- **Previews**: the app previews of every project (see below), with their URL, *Make public* / *Make private* and a button that stops each.

### Actions

- Stack and each service: *Start* (`up -d`), *Stop*, *Restart* (same container), *Recreate* (`up -d --force-recreate`), *Rebuild* (`up -d --build`), *Pull* (`pull` then `up -d`); the stack also has *Down* (confirmed) and *Down with volumes* (`down -v`, the project name typed to confirm). Start, stop and restart run in the pod (the row shows a spinner, an error a toast); the others run as tasks in the Console, their output visible.
- Containers of the Host tab: start, stop, restart, remove (confirmed, `rm -f`), logs, shell.
- *Shell*: a Console terminal running `docker exec -it` with `bash`, or `sh` when bash is missing; *Shell as root* adds `-u root`.
- The lists refresh every few seconds while the tool is shown.

### Detail pane

- **Infos**: image, state and health, command, creation date, ports (`host:port → container port/protocol`), mounts (type, source → destination, read-only), networks (name, IP address, aliases), and the live values: CPU, memory (usage / limit), network and block I/O, processes. Instant values from `docker stats --no-stream`, refreshed every few seconds while shown.
- **Logs**: the last 500 lines, then followed live (`docker logs -f`, `docker compose logs -f` for the Stack row, with a service filter). Text filter, timestamps shown or hidden, ANSI colors kept, scrolling up pauses the following, at most 10,000 lines kept.

### Port forwards (SSH projects)

- A port forward makes a port reachable from the SSH host (`127.0.0.1:5432` as seen by the host, or another address) available on the machine of the pod, like `ssh -L`. Local port: the remote one by default.
- Scope per port forward: *this machine* (`127.0.0.1`, default) or *local network* (`0.0.0.0`, flagged with a warning: any machine of the network reaches the service, without authentication of the pod).
- Added by hand (remote address and port, local port, scope); each can be stopped, started or deleted. Kept in `.ide/forwards.json` (`tunnels.json` in older projects: read, then moved); the enabled ones open when the project opens.
- They stay open while a window of the pod is open (the home page included), whatever the project; 5 minutes after the last window leaves, they close. The home page lists the open port forwards with a button to close each and *Close all*.

### App previews

- The assistant offers to try an app with `share_preview` (title, command of the development server, its port, optional folder): a card in the conversation. Nothing runs before the click. A saved `serve` command of the project comes first: `command_id` names one (its command, folder and port), no command takes the only one with a port (several: the model is asked to choose), and a command written as a saved one is linked to it. A saved command runs in its own console (one per command: a console of it still running is reused).
- *Start and open the app*: the pod runs the command in a console of the project (the worktree of a development), shown in the window, unless the port answers already; waits for the port (60 s at most, an error says why), then the app opens in a new tab. A second click opens the running preview.
- Each preview is a listener of the pod on `127.0.0.1` proxying to the port of the app on the machine of the project (through SSH for a remote one), WebSockets included (hot reload); the app sees `localhost:<port>` as its host and gets the cookies of the browser (the IDE's too: the app may be a Web IDE in development), not the cookie of a public preview; its redirections to its own address stay on the preview.
- With Tailscale (its `tailscale` command), the preview is served over HTTPS on the tailnet, one port per preview from 8401 (`tailscale serve`), reachable from any device of the tailnet. It is private: the cookie of the IDE is required, anyone else gets a page saying so. *Make public* opens it to the internet with Tailscale Funnel on 8443 or 10000 (so two public previews at most; Funnel must be allowed by the tailnet policy): its link carries a token, exchanged for a cookie at the first visit. Without Tailscale, the preview stays on `127.0.0.1`, which the card says.
- A preview stops with its command (console closed or command ended), after 24 hours, or with *Stop* (which also closes the console); its Tailscale ports are turned off.

### Assistant

The assistant reads, without confirmation: the state of the services of the project (`docker_ps`) and the logs of a service or container (`docker_logs`, last lines, optional filter). It runs no Docker action itself.

## 15. Decisions

- **Q1**: the pod pushes the whole file (`fs.changed`, decoded to UTF-8 with LF line ends, with its encoding and line separator) on each change, after 150 ms of stability (AI tools often write in several steps).
- **Q2**: "accept both" concatenates local then remote.
- **Q3**: credentials are never written in `.ide`. Remembered passwords go to `~/.web-ide/secrets.json` (mode 0600, same model as `~/.pgpass`, not encrypted); otherwise they stay in the pod's memory for the session.
- **Q4**: Redis keys get a dedicated node (type and TTL) under each `dbN`.
- **Q5**: systemd user unit (`make service`) or one-off run (`make run`). No tray icon.
- **Q6**: the pod listens on `127.0.0.1` and refuses other machines. `-allow-remote` allows remote access, protected by the token only.
