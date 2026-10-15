# Changelog

All notable changes of Web IDE. The release workflow publishes the section of a version as the notes of its GitHub release.

## [Unreleased]

### Fixed

- TURN over TLS on 443 now works for a client that names no ALPN protocol (#74). Pion, the WebRTC stack of the pods, dials it without one: the relay sent it to HTTPS, the allocation never came, and a pod held its answer to every browser for the 8 s cap of its candidate gathering. The relay now reads such a connection after the TLS handshake — a STUN message goes to TURN, anything else to HTTPS — and routes each connection off its accept loop. The answer of a pod comes in about 150 ms instead of 8 s.

### Added

- The page of the relay is kept by its Service Worker (#73): the bootstrap page, its scripts, the identity module, the wordlist and the installable app are precached, and the page of a pod is kept under the build of its front end (a hash the pod tells in its handshake). A reload through the relay fetches no static file — it pays the connection only. A new shell on the relay installs in the background and the page offers a **Reload** bar; with the relay down, the cached page says so.
- The journal of a connection through the relay runs to the home page of the app (#75): the app tells its start, its first message from the pod and its home page, and the journal stays readable once the app is open — Settings > Remote access > Connection journal, with Copy.
- The connection card of the relay page says what each step does (#74): the sub-step it waits for and since when, the candidates and the path ICE chose, and a **Details** journal with times and a Copy button. A sub-step that stalls says what blocks and why, with Retry. The pod logs one line per tunnel step (offer, answer, ICE, handshake).
- The footer of the relay page offers **Install the app** where the browser hands it its install prompt, and the Share › Add to Home Screen hint on iOS.

## [1.13.1] - 2026-10-15

### Changed

- A relay with a domain advertises the domain alone in its ICE URLs: TURN over UDP on 3478, and TURN over TLS on 443 as the fallback of a network that blocks UDP. Each TURN URL costs a relay port on each end of a tunnel; the hosted relay advertised its domain, IPv4 and IPv6, each over UDP, TCP and TLS — up to 9 per end. A session now costs 4, about 710 simultaneous sessions on the hosted relay instead of about 236. A relay without a domain is unchanged.
- The hosted relay relays on 2840 ports (`-turn-ports 49160-51999`, was 840), and `deploy-relay.sh --setup` writes a firewall rule ufw accepts for the range (it refused `--force` with a range), replacing the old one.
- docs/load.md: the numbers of the relay under load (#72) — pages, signaling and TURN tunnels up to 500 sessions — and the wall it runs into first, its relay ports.

## [1.13.0] - 2026-10-15

### Added

- The hosted relay at **antennae.space**: a relay you can use without running one. A pod with no relay connects to `wss://antennae.space` (Settings > Remote access, where the URL is edited and the link turned on or off while the pod runs); the page of the relay pairs a device and opens its pods.
- A monthly TURN quota per account (10 GB on the hosted relay): the bytes relayed are counted per device key and aggregated per month in the base of the relay. Over the quota a browser gets no TURN credential — the bootstrap page says the quota is reached, Settings > Remote access tells the same month, and the pod is told the same figure. `-quota 10GB` sizes it on a self-hosted relay.
- GitHub Sponsors: a pod account linked to a login on the sponsor list of the repository is shown lifting the quota, and its credentials are issued over it. `-github-env` gives the relay the credentials of the check.
- `/_relay/status`, anonymous JSON: what the relay answers (up, version, uptime), the pods online, the accounts seen in the last 30 days, and what it carries — pod, browser and identity links, TURN allocations open, the size of its relay port range, resident memory, file descriptors, goroutines.
- TLS of the relay on both stacks: `-domain antennae.space` gets the certificate from autocert (certbot not needed), the HTTP and HTTPS ports answer on IPv4 and IPv6, and TURN is advertised on both addresses. A strict Content-Security-Policy on every page of the relay, HSTS on the TLS answers.
- `deploy-relay.sh --setup` brings a host up from nothing (service user, data folder, unit with the flags of the hosted relay, firewall, unattended upgrades, nightly backup timer), idempotently; `scripts/relay-watch.sh` runs off the host — it pulls the backups (30 days kept), checks `/_relay/status`, and installs the two user timers.
- `make load-relay` / `make load-relay-down` (#72): a load test of a relay. `pod/internal/relayload` drives one over its own protocol — HTTP and WebSocket links, WebRTC tunnels with the token handshake and a payload pumped through them — in three tiers (pages, signaling, TURN), sessions ramped then held, `/_relay/status` read while it happens. `scripts/relay-load.sh` installs a second relay on the same host (its own unit, data folder, ports and a narrow TURN range) so the hosted one is never touched.
- `deploy-relay.sh --check`: the flags of the unit's `ExecStart` are looked for in the help of the new binary, on the host, before anything is replaced — `Restart=always` turns a binary that does not know a flag into a crash loop.

### Changed

- A tunnel of the load generator comes up in 2.9 s instead of 22.8 s: the offer no longer waits for the ICE gathering to finish, candidates are trickled as a browser sends them, and those that arrive early wait until the peer connection they belong to exists.
- The pod, browser and identity links refuse a frame over 1 MiB.
- docs/relay.md describes the hosted service as it runs, docs/self-hosting.md is a guide for running your own (domain and DNS, flags, systemd unit, firewall, backups, moving a pod to another relay).

## [1.12.0] - 2026-10-14

### Added

- `make deploy-relay`: the hosted relay (antennae.space) updated from the current tree — built for Linux x86_64, installed on the host, its service restarted, and the Service Worker it serves checked against the build. `RELAY_VERSION=v1.11.0` deploys a published release with its checksum, `RELAY_ROLLBACK=1` goes back to the previous binary kept on the host. The data of the relay (its base, its VAPID key, its certificate) is never touched.
- Phone: the page never zooms. `touch-action` on the document and a guard on the start of a two-finger touch: pinch and double-tap zoom are refused everywhere in the window, the overlays mounted outside the app (menus, modals, popups) included; the doodles and the board keep their own pinch. The pages served by the pod (pairing, *update this pod*, errors of a preview, the bootstrap page of the relay) are fixed too.

### Fixed

- Phone: the chat no longer zooms when the message box is edited. iOS zooms a focused field showing text under 16 px: on a phone every editable (the message box, the editors, the fields, the terminal) takes 16 px at least, a size of code this device chose above that still applying, the gutter of the editor keeping the size of the lines. A zoom the system imposes anyway is undone by re-applying the viewport.

## [1.11.0] - 2026-10-14

### Added

- Multi-pod protocol versions: each pod tells which multi-pod protocol it speaks, the home page marks a pod of another one (*older version* or *newer version*), and nothing crosses the gap — no route, no gateway of the site, no project link.
- *Update this pod* on the home page: a pod running a release offers an outdated pod the version it runs itself. The browser signs the request with its device key; the pod it names checks it against the account it pinned and its revocation list, then runs the installer it installed, outside the process it restarts. A pod that does not run as the service is given the command to run there by hand.
- Push notifications through the relay (Settings > Notifications, on the relay origin): the agent finished or asks for you, tests failed, feedback on a ticket; a switch per kind and per device, nothing while the device shows a window of the pod, a click opens the conversation or the ticket. The pod encrypts each notification for the browser (RFC 8291): the relay signs (VAPID) and forwards ciphertext only, to the registered endpoints of signed, non-revoked browsers of the same account.
- The bootstrap page of the relay installs as an app (PWA) and remembers the last pod.
- Agents tool: Claude Code, opencode and pi in tabs through the Agent Client Protocol, beside the assistant. Streamed messages, thoughts, plan, tool calls with their diffs, permission cards, mode / model / effort of the agent, Stop. The adapters are installed by the IDE on demand. A session runs in the keeper and survives the pod; an agent that ends is restarted with its session loaded.
- MCP of the IDE: the tools of the assistant for Claude Code and the CLI agents — consoles (`list_consoles`, `read_console`, `console_input`), Docker (`docker_ps`, `docker_logs`), previews (`share_preview` offered with a click, `list_previews`), and the window: `open_file`, `open_diff`, `focus`, `show_message`, `request_doodle` (the user draws, the agent gets the image).
- Work time and cost of the agents: per session (tab tooltip), per ticket (badge) and per project (Agents launcher, Infos tool) over today / 7 days / all; tokens per model from Claude, opencode and pi, priced by a table kept in the pod (`<data>/prices.json` to add models), shown as an API equivalent under a subscription.
- Agents tool: the message box of the assistant for the sessions — `@` files, `/` the commands the agent announces, images, files and doodles sent as ACP content blocks, a queue for the prompts sent during a turn (kept by the pod), ↑ / ↓ history, the agent's mode and model as the mode and model buttons of the assistant, the context ring and its popover (session time and cost, *Compact now*), full screen, the phone layout.
- Kanban: a *Development agent* setting (assistant, or Claude Code / opencode / pi of the Agents tool). With an agent, *Start development* opens one session per ticket in its worktree, briefed with the ticket; corrections go to the same session; the ticket opens it; removing the worktree ends it. Every agent session gets the MCP server of the IDE (kanban tools), and `kanban_get` / `kanban_conversation` / `/review-dev` read the session.
- Questions tool: every question waiting for you (permissions and forms of the Agents sessions — Claude's AskUserQuestion included —, `ask_user` of the assistant, and a new MCP `ask_user` for the CLI agents) in one list, and a modal with the last message of the agent that opens by itself when the question is not on screen (a setting, an override per session); a push notification on a new question.
- Agents tool: a follow mode per session (the eye of the message box) opens in the editor each file the agent edits, for Claude Code, opencode and pi.
- The Service Worker of the relay keeps the assets a pod serves as immutable: one cache per pod and per version of that pod, read before a fetch is carried through the tunnel, written under a cap of 8 MB per entry, dropped when the pod declares another version or after a month. A reload of the page no longer carries the app: 1.5 MB and 4 channels become 1.7 kB and 2. The page itself, its `/api` calls and the previews always go through the tunnel, so a cached asset never stands for a live connection.
- What a load of the relay page costs is measurable: `window.bootTimings` (the Service Worker, the device, the identity link, the signaling, the handshake, the app) and `window.httpChannels` / `window.httpBytes`, printed by the `relay` suite on the first load and on a reload.

### Changed

- A project of another pod of the LAN opens under `/_lan/pod/<key>/` of the local pod, not on a port of its own: it stays in the window of the installed app, with no warning bar of an outside site.
- On the relay page, the tunnel and the Service Worker start together: the page no longer waits for the (slow, once per browser) activation of the Service Worker before dialling the pod. A first load reaches the tunnel in 32 ms instead of 2011 ms, a reload opens the app in about 650 ms.

### Fixed

- Agents tool: *Install* failed with `ENOTEMPTY` after an interrupted npm; the IDE now removes the folders it left, runs one install at a time and shows the npm errors without its warnings.

## [1.10.0] - 2026-10-12

### Added

- The IDE installs as an app (PWA) from Chromium browsers: its own window, where shortcuts such as `Ctrl+Tab`, `Ctrl+W` or `Ctrl+N` reach the IDE.

### Changed

- Assistant: a compaction (automatic, `/compact` or asked by the model) summarizes all but the last 10 steps; one long turn alone can be cut. Every compaction keeps its card in the conversation, the last one open on its summary.
- Assistant: the reasoning of the current turn goes back to the model; an answer without text nor tool call ends the turn with an error.
- e2e: `run.sh -j N` runs the suites in parallel workers.

### Fixed

- Passkeys also offer ES256 and RS256: Google Password Manager, iCloud Keychain and Windows Hello refused an Ed25519-only passkey.

## [1.9.0] - 2026-10-12

### Added

- Remote access through a relay (`web-ide-pod relay`, open source and self-hostable): signaling, STUN and TURN (also over TLS on 443), a bootstrap page and a Service Worker. A pod started with `-relay` keeps one outbound WebSocket to it, with no port to open; from any browser on another network, the app, its terminals, its file transfers and the app previews come from the pod over WebRTC DataChannels, end to end encrypted. The token is proved inside the tunnel, bound to both DTLS fingerprints, and never reaches the relay. Settings > Remote access shows the link and its state.
- Accounts without e-mail or password: `web-ide-pod pair create` makes a 24-word recovery phrase, shown once (numbered) and never stored, with three words asked back; `pair restore`, `pair join` (a code, a terminal QR and a 4-word fingerprint), `pair status`. Each pod and each browser has its own device key and a certificate signed by the account key; every connection is a mutual proof between two certificates, so a relay that swaps an offer is refused.
- Settings > Remote access > Devices: pair a browser with the phrase (its keys unlocked by a passkey, WebAuthn PRF; recent browsers only), list the devices of the account with their fingerprint, approve a pairing request, invite a phone by QR, revoke a device (a signed list, checked by every pod).
- The pods of one account find each other on the local network with no relay: mDNS, or an address typed in Settings > Remote access > Local network. The home page shows one Projects block per pod of the account; its projects open through this pod, their page and terminals coming from the pod that holds them. A paired pod opens a LAN port (`:4436`, `-lan`); only the pods of the account are listed, each one proving its certificate bound to the TLS link.
- Pairing and revocation without a relay: `pair join` waits on the LAN and is approved in Devices on `localhost`, `pair join --phrase` joins under its own key, `pair revoke <key>` signs a list from the phrase typed. A revocation spreads from pod to pod and reaches the relay as soon as a pod of the account does.
- Markdown files open rendered in the editor: a floating button at the top right (or Alt+M) shows the source and back at about the same place, a double-click on a block shows its line in the source. Links open the files of the project, anchors scroll to their heading, images of the project are shown.

### Changed

- A new editor tab opens first, on the left of the bar (a tab already open keeps its place). The active editor tab has an inner border, thicker at the bottom.
- A tool given the focus by its shortcut keeps it when its rows are rendered again a moment later.

## [1.8.0] - 2026-10-10

### Added

- A shell command of the assistant looks as in the Console: its prompt line with a button to copy the command, its output with its ANSI colors (kept by the pod for the page, the model still reads the plain text), and its exit code in its row (red when it fails).

### Changed

- The reasoning and the tool calls of the assistant are joined in one block across its answers, until a text or a card. The last block of the conversation stays open; each one above it is folded behind a label with its number of actions, its failures and the time spent on it (generation and tool runs), opened and folded back by a click.
- The summary of a tool call no longer shrinks to nothing next to a long target.
- Roadmap of the kanban: no more column of row labels; the lineages linked by dependencies are grouped as a staircase (each one right below the one it waits for), with a gap around each group.

## [1.7.0] - 2026-10-10

### Added

- Word wrap in the editor (setting: Markdown and text files by default, every file, or never; *View › Wrap the long lines* or `Alt+Z` for the open document): long lines wrap at the width of the editor; the gutter, the carets, the folds and the guides follow the rows.
- Markdown lists continue on Enter (`- `, `1. ` numbered on, `- [ ] `); Enter on an empty item ends the list.
- The message box of the assistant is a Markdown editor: the font and the editing of the code editor (highlighting, word wrap, multiple carets, `Alt+J`, undo), Enter adds a line, `Ctrl+S` or `Ctrl+Enter` sends; `@paths` and `/commands` are highlighted. A full screen mode (`Ctrl+Shift+E`, Esc to leave) gives it the whole tool.
- LED strips around the ticket bar of the message box show the mode (Orchestrator green, Build yellow, Briefing orange, Plan blue, violet during a compaction): brighter with the focus, pulsing while the assistant works.
- Complexity of a ticket and of a test feedback (low, medium, high), estimated by the model with the plan and the feedback it adds, editable: it routes the sessions. *Briefing*, *Start development*, *Fix session* and *Fix feedbacks* run the integrated assistant with the effort of the complexity (Max, Dynamic, Low) on a click, and list every level with Claude Opus, Sonnet or Haiku; *Review* offers Opus or Sonnet.
- The cards of the board list the titles of their goals and show the time of the answers of their conversations; the ticket view shows it next to each conversation (sub-agents included, the thinking in the tooltip).

### Changed

- The message box of the assistant is docked at the bottom of the tool like an editor pane, its buttons in the order Mode, Dictate, Attach, Options, with a short placeholder (*Message…*).
- The ticket bar of a conversation sits on top of the message box: *Ticket #n*, its status, and the role of the conversation on the right.
- Plan, Briefing and Orchestrator write scratch files in `/tmp` and run the commands confined to it freely; Build changes the project and `/tmp` without asking.

### Fixed

- Build mode: a known program called by its path in a bin folder (`~/sdk/go/bin/go test`, `/usr/bin/env make`) runs freely like the same program called by its name; the prompt asks the model to call the programs by name, a variable such as `$GO` still asking.

## [1.6.0] - 2026-10-10

### Added

- *Review* of a ticket (*In progress*, *To test*): Claude Code (Opus) reviews the change of its branch, runs the tests, adds each finding as a test feedback and the verdict as a note (MCP prompt `review`). A split button: the integrated assistant will join it.
- Split buttons: *Briefing*, *Generate the plan*, *Start development* and the *Fix session* of a feedback start the integrated assistant on a click; their arrow offers Claude Code instead, in a terminal. The MCP prompt `fix` takes the id of one feedback.
- *Fix feedbacks* replaces the validation of a ticket while a test feedback is open (one session for all of them, assistant or Claude Code); the pod refuses to close the ticket or validate its step until they are handled.
- The model adds test feedback (`kanban_feedback` action `add`) and edits or deletes goals (`kanban_goal` actions `edit`, `delete`), for the assistant and Claude Code.

### Changed

- The ticket view lists the sections of its status first, then, under a rule, those of the earlier statuses, folded; lineage, linked files, attachments and history aside. A closed ticket keeps the sections with content.
- The header of a ticket keeps its main actions (*To test*: *Add feedback*, *Review*, *Validate*); the others, *Open the worktree* among them, are in *More actions*. The *Claude Code* button is gone: its actions are in the split buttons and *More actions*. *Close the ticket* is now *Validate the ticket*, shown only once its lineage is finished, else *Validate the step*.
- The priority and the size of a ticket are fixed once its development started: badges in the view, refused by the pod (a plan written later keeps the size).
- The pull request section shows once a pull request exists or can be made.

### Removed

- Linked commits (the section, `kanban_link_commit`): *Git and changes* shows the commits of the branch.

### Fixed

- The context of the llama-swap models survives a restart of the pod (kept in `llm-contexts.json`).

## [1.5.0] - 2026-10-09

### Added

- Claude Code answers the local assistant: the MCP tools `kanban_reply` (a message in a conversation linked to a ticket) and `kanban_answer` (the `ask_user` questions waiting); `kanban_conversation` lists the questions waiting. The thread marks what Claude wrote or answered.
- The Orchestrator adopts a conversation that runs on its own (`agent_adopt`), and follows from their start the developments its cards start: such a conversation becomes its sub-agent, announces itself with a note, keeps working with the user and reports when its task is over.
- The Orchestrator resumes a conversation whose last answer failed (`agent_resume`), from its last completed step, and adopts it on the way.
- `kanban_conversation` (MCP) reads the last messages only (`last`) with the tool calls, errors and notes (`tools`); the skill `/review-dev` of this repository reviews the developments in progress with it.
- Reasoning effort of the assistant (options of the message box): *Dynamic* by default, or fixed at *Max*, *Medium* or *Low*. Dynamic thinks the most after a message of the user, medium after tool results, low after a step of simple edits or bookkeeping that succeeded, the most again after a failed or refused call. The *Effort tool* switch offers `set_effort` to the model, which then chooses the level until the user writes again. The block of the reasoning shows the effort of the answer. Sent as `reasoning_effort` in `chat_template_kwargs` (Qwen 3.8 templates; Strata needs `"effort_position": "end"` to keep its conversation cache when the effort changes).
- A server of kind *OpenAI-compatible* that is llama-swap is treated as the local servers behind it: its models can think (the *Thinking* option and the effort show), and it gets the fields of llama.cpp (`chat_template_kwargs`, the progress of the prompt reading).
- Statistics of the assistant, a view next to the board (tabs *Board* / *Statistics* in its column, a button in the header): speeds of reading and writing, cache, time generating and thinking (with the estimated share of the thinking tokens), by reasoning effort, time and failures by tool, answers calling several tools at once, repeated calls, the context now estimated by part, its curve and the compactions. *This conversation* adds its sub-agents (each detailed); *Whole project* filters by model, effort and period. Followed live during an answer.
- Each message of a conversation keeps when it came (`at`); a tool call keeps its wait for the approval (`waitMs`) and the kind of its failure (`failure`: `usage` for a wrong call — a parameter missing or invalid, an unknown tool, an `old_string` not found —, `exit` for a command ending with a code, `error`).
- The local terminals survive the updates of the pod: they run in a second service, the keeper (`web-ide-keeper`, installed by `install.sh` next to the pod, replaced only when its protocol changes). Restarting the pod no longer ends the shells; the page reconnects and shows their output from where it was, without gap or duplicate. Without a keeper (`-keeper off`), the pod runs them as before.
- The conversations of the assistant survive the updates of the pod too: the answer being written goes on through the keeper (an HTTP relay keeps it), a command of the agent running (`bash`, `run_command`) is followed again, an approval waiting is asked again, without asking the model twice. The keeper speaks protocol 3.
- The terminals and the commands of the agent of an SSH project run in the keeper too, on its own connection (the host key accepted by the pod, pinned): they survive the updates of the pod.
- The keeper updates itself in place (`web-ide-keeper keeper -upgrade`, used by `install.sh`): it re-executes its new binary in the same process, keeping its local terminals and their output; it waits for the answers being written and the SSH commands (or cancels them with `-force`), closes the SSH terminals, and stays as it was when the update fails. A keeper older than this one is restarted once.

### Changed

- The conversations of the assistant: the last Orchestrator conversation first with its working children, then the other active conversations as trees, then the history by day; an ended sub-agent leaves its parent for the history of the day it ended.
- An orange dot marks a conversation waiting for an answer, a red one a conversation whose last answer failed; both stay among the active conversations. *Abandon* leaves a failed conversation (a sub-agent is stopped, its parent told). The older Orchestrator conversations with working children keep their tree at the top.
- Worktrees open in the same window: the worktree selector (and *Start development*, *Open the worktree*) switches the explorer, the search, the Git tool and the new consoles without reloading; tabs and consoles of several worktrees stay open side by side, each with a chip (`#<n>` or the branch). The selector shows *Ticket #<n>*; *Open in a new window…* keeps the former way. A development conversation keeps working in the worktree of its ticket.
- The steps of an answer are blocks: the reasoning like the tool calls, each with its duration, counted while it runs. The running block is open and follows its stream (the reasoning, the output of a command as it comes); it folds once done, unless the user toggled it.
- The system prompt asks the model to put the tool calls that do not depend on each other in one answer: fewer round trips, and less thinking between them.

### Fixed

- git run by the assistant takes `auto` as its comment character: a `git rebase --continue` no longer strips the `#<n>` subject of the ticket commits.

## [1.4.0] - 2026-10-07

### Added

- Sub-agents: the assistant delegates a task with `spawn_agent` to a child conversation (fresh context, same rights) running in the background; the child notes its progress, asks its parent and reports, its questions and report waking the parent, which replies (after asking the user if needed), writes to or stops it. What a child needs confirmed goes to the user. Cards in the parent thread, children nested under their parent in the history.
- Cloud providers: a server kind *OpenAI-compatible provider* (OpenAI, OpenRouter…) with its API key kept on the pod, models listed or typed by hand, reasoning and cost read from the stream, rate limits retried and errors explained. Servers can be offered to sub-agents with a note; the assistant picks the server and model of a child (default in the new *Sub-agents* settings tab), whose card shows its tokens and cost.
- Orchestrator mode, the default of a new conversation: it tells what to work on next (`kanban_next`) and what was done (`kanban_history`), offers actions as cards the user clicks (start the development, generate the plan, open a ticket or a conversation), and moves the user into the right conversation (`open_conversation`: an idea goes to a Briefing). It changes no file; its sub-agents may delegate once more.

### Fixed

- A message queued just as a conversation ended stayed in its queue: the conversation now starts again with it.

## [1.3.0] - 2026-10-07

### Added

- Phone layout (720 px wide or less): one bar of three rows with the menus and a single row of icons (the editor first, then the tools), one view at a time full screen, the editor brought back when a file is opened and locked until a double tap (a padlock locks it again), no field focused by itself, the caret line kept above the keyboard, and no zoom of the page.

## [1.2.0] - 2026-10-06

### Added

- Whiteboard of a conversation: its doodles as read-only pages, next to the conversation in a wide assistant or in its place in a narrow one, with zoom, pan, thumbnails and *Reuse*; *Show on the board* from a doodle of the thread.
- The assistant draws on the board: `board_draw_doodle` (layouts, flows, sketches, or annotations on a clone of a page) and `board_draw_image` (an SVG it writes, an image of the project, or a capture of the screen shared by the user); it reads back the description and the image of each page, and its pages go to the tickets like the doodles. The images sent in a conversation are pages of its board too.
- App previews: the assistant offers to try the app with `share_preview`; a click starts its command, waits for its port and opens it on a temporary HTTPS URL of the tailnet (private, the cookie of the IDE required), or public with Tailscale Funnel; it stops with its command. Listed in the new *Previews* tab of the Docker tool.

### Fixed

- A transparent GIF or WebP put under a doodle had a black background: only JPEG images are kept as JPEG.

## [1.1.0] - 2026-10-06

### Added

- Claude Code on the kanban: an MCP endpoint of the pod (`/mcp`) gives Claude Code the tools of the assistant on the tickets, the conversations of a briefing, the start of the development, and the commands `brief`, `plan`, `dev` and `fix`; its changes are shown as written by Claude.
- *Claude Code* menu of a ticket (brief, plan, develop, fix) running Claude Code in a terminal of the IDE; the setup command in the kanban settings.
- Claude Code mod (`claude-mod/`): links to the project and to the ticket of the worktree, and the files changed by Claude, above the prompt, opened in the IDE in one key; `/webide <file[:line]>`.
- `publicUrl` (config, `-public-url`): the address of the IDE in the links given to Claude Code (a Tailscale name…); `/project/<id>?ticket=<n>` opens a ticket.
- File links (`/open?path=…&line=…`): a click on a file mentioned by Claude Code opens it in the IDE.
- Lineages of tickets: the steps of a ticket are developed one after the other in its worktree and on its branch, each one once the previous one is validated, and the ticket is merged once they are finished; a ticket may wait for tickets of other lineages (merged or done). Blocked starts are refused to the models and can be forced by the user; badges on the cards, a *Lineage* section in the ticket, `parent` and `depends_on` in the kanban tools.
- The agent of the assistant runs in the pod: a conversation goes on with its window closed (or the phone asleep) and shows up again when a window opens, several conversations run at once (a number per model server, the others wait in a queue shown in the conversation and the history list), and every window following a conversation can write to it, answer its questions or confirmations, or stop it.
- Question types of `ask_user`: an idea to validate (swipe on a phone), two approaches compared side by side, items to rank (or a top N), a concrete scenario, or choices with their pros and cons; on any question "I don't know", "Up to you" and a note. A call breaking the rules of a type is refused with an error naming the question.
- Graphs of questions in `ask_user`: a follow-up is asked only when the answer leading to it is chosen, with a breadcrumb; leaving the anticipated path (free answer, "Yes, but…", "I don't know") sends the round at once so the model rethinks.
- Roadmap view of the kanban: a row per lineage, blocks as wide as the estimated size of their ticket (S, M, L, XL, written with the plan), what can start, what is blocked, and the dependencies between lineages at a glance.

### Changed

- A stopped or failed answer of the assistant resumes from its last completed step (*Resume*) or starts again from the user message (*Retry*).
- The counters of an answer show the speed of the prompt reading and the share of the prompt found in the cache of the model server.
- Reloading the page or closing it no longer stops a conversation, and a second window following it can write to it instead of being read-only.

## [1.0.0] - 2026-10-05

First release.

### Editor and project

- Block editor for large files (100,000 lines), syntax highlighting with the CSS Custom Highlight API, split panes sharing buffers, multiple carets, folding, indentation guides, find and replace, encodings and line separators, QWERTY and AZERTY shortcuts, themes (including high contrast) with keyboard navigation of the whole interface.
- File explorer with file type icons, git colors and folder marks; Search Everywhere, Recent Files, the switcher and a clipboard history.
- Three-way merge of the changes made to open files by other programs, with a resolution dialog for real conflicts.
- Code navigation through language servers: definition, references, implementations, symbols, completion, rename, formatting, diagnostics.
- Local and SSH projects, project icons, the same session in every browser.
- Terminals and commands, detachable into their own windows.
- Git panel: status, staging, commits, branches, history graph, side-by-side diffs, worktrees.
- Database explorer for SQLite, PostgreSQL and Redis; Docker tool (Compose stack, containers, logs, disk usage) and SSH tunnels.

### AI assistant and kanban

- Agent for llama.cpp and Ollama servers: reads, searches and edits the project, uses the language servers, runs commands, asks questions, follows `CLAUDE.md` / `AGENTS.md` and skills; Build, Plan and Briefing modes; context compaction; Mermaid diagrams; image, PDF and audio attachments; local dictation (Whisper in the browser); doodles to show a layout or a flow.
- Kanban per project: briefing, plan, development in a git worktree per ticket, test feedback, merge, rebase and pull request, each step with its linked conversation.

### Install

- Single binary with the front end embedded; Linux and macOS archives.
- `scripts/install.sh` installs a release as a systemd user service started at boot, optionally served over HTTPS on a Tailscale network.
- English and French interface.
