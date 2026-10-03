# Handoff · Web IDE

État au 2026-10-03. À lire avec `Claude.md` (spécification complète, décisions Q1-Q6 en section 13, notes d'implémentation en section 14).

## Où on en est

Toute la spec est implémentée et testée, plus sept ajouts : autocomplétion, renommage, formatage, panneau Git, assistant IA, transcription vocale locale et fonctions d'agent (instructions CLAUDE.md / AGENTS.md, skills, compaction, outils IDE). Tout est commité sur `main`.

| Commit | Contenu |
|---|---|
| `02ddfb7` | Pod Go + front SolidJS (toute la spec) |
| `79a71a0` | Éditeur découpé en blocs (perf gros fichiers) |
| `191dde6` | Tests e2e, tunnel SSH testé, service systemd |
| `e647ede` | Autocomplétion, renommage (Maj+F6), formatage (Ctrl+Alt+L) |
| `e336135` | Panneau Git, onglet de diff, marqueurs de gouttière |
| `56159ea` | Assistant IA : chat llama.cpp / Ollama, outils fichiers + LSP |
| `dcd370f` | Transcription vocale locale (Whisper dans le navigateur) |
| `fb780dd` | Agent : CLAUDE.md / AGENTS.md, skills, prompt éditable, compaction, outils IDE et consoles, conversations en SQLite |
| dernier commit | Nouvelle interface de l'assistant, historique latéral, Mermaid et stats en direct, conversation restaurée |

## Commandes

```
make build          # front (Vite) puis binaire bin/web-ide-pod (front embarqué)
./bin/web-ide-pod   # http://127.0.0.1:4433/?token=… (jeton dans ~/.web-ide/token)
make dev            # pod -allow-remote sur 0.0.0.0:4433 + Vite 0.0.0.0:5173 (HMR)
make test           # go vet + go test + tsc
make e2e            # tests navigateur (toutes les suites, ~3-4 min)
./e2e/run.sh git    # une suite : editing features restore+ git lsp llm agent speech perf
make service        # service systemd utilisateur (pas activé à ce jour)
```

Go 1.27 est dans `~/sdk/go/bin`, pas dans le PATH ; le Makefile le trouve tout seul. gopls est dans `~/go/bin` ; le pod a besoin de `~/go/bin` **et** de `~/sdk/go/bin` dans son PATH pour la navigation Go (gopls appelle `go`).

Tests DB optionnels sur de vrais serveurs : `WEBIDE_TEST_PG=hôte:port:user:mdp WEBIDE_TEST_REDIS=hôte:port:mdp go test ./internal/db/` (validés avec des conteneurs Docker temporaires, supprimés depuis).

## Architecture en bref

- `pod/internal` : `server` (HTTP, pairing, RPC WebSocket, files séquentielles + barrière LSP), `runtime` (un par projet ouvert), `fsx` (local/SFTP), `sshx`, `execx` (processus locaux ou SSH), `console`, `lsp`, `db`, `git`, `search`, `llm` (serveurs de modèles, chat, conversations), `hfcache` (modèles Hugging Face servis depuis un cache disque), `sshtest` (faux serveur SSH pour les tests).
- `web/src` : `editor/` (Doc, EditorView en blocs, tokenizer + grammaires, merge, linediff, FindBar), `state/` (project, settings, git), `keys/` (presets JSON + bindings), `lsp/` (client, completion, edits, refactor), `ui/` (EditorArea, Completion, DiffView, overlays), `panels/`, `tools/`, `db/`, `console/`, `llm/` (assistant IA), `pages/`.
- `e2e/` : `run.sh` lance un pod neuf par suite (données et workspace temporaires copiés de `e2e/fixtures`), Chromium du cache Playwright (`~/.cache/ms-playwright`) ou `CHROME=…`.

## Pièges rencontrés (à ne pas refaire)

- **Solid vide un conteneur** dont l'unique enfant est dynamique (`textContent = ''`) : tout nœud monté à la main (la vue de l'éditeur) doit avoir son propre élément, voir `.editor-mount`.
- **Callbacks de la vue sous `untrack`** (`useEditorView`) : sinon un effet qui appelle `setSelection` s'abonne à ce que lit le callback (bug : le curseur revenait à la dernière cible de navigation).
- **Blocs de l'éditeur en `width: max-content`** : sinon allonger la ligne la plus longue relance la mise en page de tout le fichier (~50 ms par frappe sur 100 000 lignes).
- **Pas de `push(...grosTableau)`** (dépassement de pile sur les gros collages) : copier en boucle.
- **Une requête LSP doit partir après les `didChange`** : `flushLsp(path)` côté front + barrière côté pod (`server.go`, `barrier`).
- **Shell** : `pkill -f <motif>` tue aussi la commande qui le lance (le motif est dans sa ligne de commande) ; utiliser `pkill -x web-ide-pod`. Lancer le pod de test avec `setsid` / `< /dev/null`, sinon un pipe reste ouvert et la commande ne rend pas la main.
- **npm** : dans le bac à sable de Claude Code le DNS du registre échoue et `npm install` reste bloqué (le lancer hors bac à sable ; un install interrompu laisse des dossiers à moitié copiés : supprimer `node_modules` et réinstaller). `typescript@latest` est TS 7 (binaires natifs), dont l'installation a bloqué ; le projet est sur TypeScript 5.
- Les tests e2e doivent attendre activement (`waitForFunction`) : gopls démarre à froid, l'index de la bibliothèque standard prend plusieurs secondes.

## Limites connues

- Navigation de code testée uniquement avec gopls ; PHP (intelephense / phpactor), Python (pyright) et TypeScript (typescript-language-server) non installés ici, donc jamais essayés.
- Testé sous Chromium seulement (Firefox non essayé).
- SSH validé avec le faux serveur des tests, pas sur un vrai hôte (le sshd local n'autorise pas la clé, `authorized_keys` non modifié volontairement).
- Recherche globale : regex en syntaxe RE2 (Go), pas de lookbehind.
- SQLite d'un projet SSH : via `sqlite3` en ligne de commande sur l'hôte, autocommit seulement.
- `make service` écrit le service mais ne l'active pas : c'est à l'utilisateur de le lancer.

## Tool « Assistant IA » (chat LLM)

Panneau de droite « Assistant IA » (détachable) : serveurs llama.cpp ou Ollama (IP:port, clé API facultative, jamais renvoyée à la page), liste des modèles avec capacités, réponses en Markdown (code coloré par les grammaires de l'éditeur, diagrammes Mermaid), réflexion repliable, jetons et vitesse, conversations enregistrées par projet.

- Pod `internal/llm` : `llm.go` (config `~/.web-ide/llm.json`, détection du type par `/api/version`), `models.go` (llama.cpp `/v1/models` + `/props`, routeur : `/props?model=` seulement pour les modèles chargés ; Ollama `/api/tags`, `/api/ps`, `/api/show`), `chat.go` (llama.cpp SSE `/v1/chat/completions`, `tool_calls` fusionnés par `index` ; Ollama `/api/chat` NDJSON avec `num_ctx` et conversion des messages), `chats.go` (`chats/<projet>/<id>.json`). RPC dans `server/handlers_llm.go` ; `llm.delta` poussé au seul client demandeur toutes les ~40 ms ; annulation par `$/cancel`.
- Front `web/src/llm/` : `state.ts` (état au niveau module, survit au changement de panneau), `agent.ts` (boucle d'outils, 30 étapes max, arrêt/relance), `tools.ts` (12 outils : fichiers, recherche, LSP ; `edit_file` = remplacement exact unique ; écritures confirmées avec diff sauf mode « auto »), `attachments.ts` (image, vidéo en `input_video` si le modèle la lit sinon 8 images, audio, PDF par pdf.js en texte ou pages en images si scanné, fichiers texte), `markdown.ts`.
- Testé : Go (faux serveurs SSE / NDJSON), suite e2e `llm` (faux serveur OpenAI scripté : outils, diff confirmé, Mermaid, historique, image, PDF, arrêt), et à la main avec le vrai llama-server (Qwen3.8-Flash-Next) : lecture, `lsp_references`, modification, relecture.
- Pièges : DOMPurify supprime les attributs contenant `-->` (source Mermaid stockée encodée en URI) et le HTML des `foreignObject` (Mermaid en `htmlLabels: false`). La fenêtre qui enregistre un serveur n'est pas dans le broadcast `llm.config` : elle applique la réponse elle-même.
- Limites : vidéo native et audio non essayés sur un vrai modèle ; Ollama testé seulement avec le faux serveur (aucun modèle installé ici). Le llama-server local n'a qu'un slot (`--parallel 1`) partagé avec d'autres clients : une requête peut attendre longtemps (« En attente du modèle… ») quand un autre client y envoie un long prompt.

## Fonctions d'agent de l'assistant

- **Prompt système** (`web/src/llm/prompt.ts`) : modèle éditable (onglet « Prompt et instructions » des réglages) global (`~/.web-ide/system-prompt.md`) ou du projet (`.ide/system-prompt.md`, prioritaire), variables `{{project}} {{root}} {{host}} {{activeFile}} {{date}} {{tools}}`, aperçu du prompt complet dans un onglet. Rechargé à chaque message.
- **Instructions et skills** comme Claude Code (`pod/internal/llm/context.go`, RPC `llm.context`) : `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, puis `CLAUDE.md` / `AGENTS.md` du dossier de données (`~/.web-ide`), tous sur la machine du pod, puis `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md` du projet, imports `@chemin` suivis (hors blocs de code, profondeur 4). Skills : `.claude/skills` et `.agents/skills` du projet (prioritaires), puis `~/.web-ide/skills`, puis `~/.claude/skills` et `~/.agents/skills` ; seuls nom et description vont dans le prompt, contenu via les outils `load_skill` / `read_skill_file`. `WEBIDE_INSTRUCTIONS_HOME` remplace le dossier personnel (tests e2e : `e2e/home`).
- **Outils IDE** (`tools.ts`) : `open_file` (avec sélection de lignes), `focus` (fichier, panneau, console, problèmes), `run_command` (nouvelle console visible, attend la fin ou le délai, renvoie sortie sans séquences ANSI et code ; sans confirmation, choix de l'utilisateur), `list_consoles`, `read_console`, `console_input`.
- **Modèles** : le modèle peut changer en cours de conversation (chaque réponse garde le sien, affiché sous la réponse) ; jauge de contexte (usage du dernier appel + estimation de la suite).
- **Compaction** (`agent.ts`) : automatique au-delà du seuil (75 % par défaut) du contexte du modèle, ou bouton « compacter », ou après une erreur de contexte dépassé ; un modèle dédié peut résumer (onglet « Compaction »). Les anciens messages restent visibles repliés (`compacted`), un message `kind: 'summary'` les remplace dans l'API ; environ un quart du contexte est gardé tel quel (sinon le dernier échange). `chat.resetAt` fait ignorer les usages mesurés avant la compaction.
- **Conversations en SQLite** (`pod/internal/llm/chats.go`) : `<projet>/.ide/chats.db` (tables `chats` et `messages`, avec un `.ide/.gitignore` qui l'exclut) pour un projet local, `~/.web-ide/chats/<projet>.db` pour un projet SSH ; les anciens JSON sont importés puis supprimés.
- Suite e2e `agent` (faux serveur scripté) : prompt assemblé, instructions et skills, prompt du projet modifié, changement de modèle, les quatre outils IDE, compaction manuelle par un autre modèle puis automatique, relecture depuis SQLite.
- Piège : `<option>` n'est jamais « visible » pour Playwright (`state: 'attached'`) ; la sélection de l'éditeur se lit dans `.cursor-info`, la sélection DOM est perdue dès qu'un autre élément prend le focus. En cas d'échec, `common.cjs` enregistre `failure.png` dans `E2E_OUT`.

## Interface de l'assistant

- Fichiers : `AssistantTool.tsx` (mise en page), `Thread.tsx` (messages), `Composer.tsx` (zone de saisie, brouillon et pièces jointes au niveau du module), `Sidebar.tsx` (historique), `AssistantSettings.tsx` (fenêtre de réglages à onglets), `parts.tsx` (Markdown, diff, popover…), styles dans `assistant.css`.
- Historique : panneau latéral (regroupé par date, recherche, renommer via `llm.chats.rename`, supprimer). Par-dessus la conversation sous 720 px de large, à côté au-delà ; toujours affiché (sans bouton) dans une fenêtre détachée assez large (`route().name === 'tool'`). La fenêtre détachée de l'assistant s'ouvre en 1100×820.
- Messages : bulles utilisateur (copier, modifier et renvoyer : la conversation reprend à ce message), réponses pleine largeur, étapes d'outils regroupées au-delà de deux, réflexion repliable avec sa durée, actions au bas de chaque tour (copier, régénérer, modèle · jetons/s · durée · jetons). Écran d'accueil avec suggestions.
- Zone de saisie : hauteur automatique, choix du modèle en popover (serveur, capacités, contexte), menu d'options (outils, appliquer sans demander, réflexion, compacter), anneau de contexte, bouton rond envoyer / arrêter.
- Pendant la réponse : `llm.delta` porte `tokens`, `speed` (llama.cpp `timings_per_token`) et `promptDone/promptTotal` (`return_progress`) ; sans timings, un jeton par fragment. Affiché : lecture du prompt en %, puis état · jetons/s · jetons · temps écoulé.
- Mermaid pendant le stream : un bloc est dessiné dès que sa clôture ``` est arrivée (`data-closed`), les SVG sont gardés en cache par source et thème et remis à chaque rendu sans clignoter.
- Conversation active mémorisée par projet dans le navigateur (`localStorage`, `webide.llm.active.<projet>`) et rouverte au rechargement ; changer de projet repart d'une conversation vide.

## Transcription vocale locale

Dictée (bouton micro ou Ctrl+Espace dans la zone de message) et fichiers audio joints transcrits **dans le navigateur** par Whisper (transformers.js 4 dans un Web Worker, `web/src/llm/whisper.worker.ts` + `transcribe.ts`) : WebGPU si disponible, sinon WebAssembly. Le son ne quitte jamais la page ; un audio n'est envoyé tel quel au modèle de chat que si l'option est cochée et que le modèle écoute l'audio. Une vidéo que le modèle ne lit pas reçoit aussi la transcription de sa bande son.

- Modèles (réglage dans la fenêtre « Réglages de l'assistant ») : tiny, base (défaut), small en 8 bits ; large-v3-turbo en q4f16 si WebGPU avec `shader-f16`. Langue : auto ou forcée.
- Fichiers des modèles : la page les demande au pod (`/models/hf/<org>/<nom>/resolve/<rév>/<fichier>`), qui les télécharge une fois depuis huggingface.co dans `~/.web-ide/models/hf` puis les sert hors ligne ; liste et suppression par les RPC `models.list` / `models.delete`. Le runtime ONNX (`ort-wasm-simd-threaded.asyncify.wasm`, 27 Mo) est embarqué dans le binaire (importé par chemin : les `exports` d'onnxruntime-web ne listent pas ces fichiers), donc aucun CDN. Le binaire passe de 29 à 57 Mo.
- Suite e2e `speech` : micro simulé de Chromium qui joue `e2e/audio/jfk.wav` (domaine public, 16 kHz mono), whisper-tiny en WebAssembly ; vérifie qu'aucune requête ne sort du pod et qu'il n'y a aucun envoi HTTP. Les modèles des tests sont gardés dans `~/.cache/web-ide-e2e/models` (`E2E_MODELS`) : le premier lancement a besoin du réseau (hors bac à sable), les suivants non.
- Limites : micro seulement en contexte sécurisé (localhost ou https) : avec `-allow-remote` en http sur une IP du réseau, la dictée est désactivée (les fichiers audio restent transcrits). WebGPU et le modèle turbo non essayés (Chromium headless sans GPU) ; transcription du français non testée automatiquement.

## Prochaines étapes proposées

1. Complétion dans la console SQL (mots-clés, tables, colonnes de la connexion).
2. Recherche et remplacement dans tout le projet.
3. Essayer intelephense / pyright / typescript-language-server sur de vrais projets de `~/Apps`.

## Méthode de travail retenue

- Un commit par fonctionnalité, testée (Go + e2e) avant le commit ; messages en français, fin de message avec la ligne `Claude-Session`.
- Textes de l'interface et messages en français, commentaires du code en anglais.
- Ajouter une suite ou des assertions e2e à chaque fonctionnalité visible.
