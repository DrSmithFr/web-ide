# Handoff · Web IDE

État au 2026-10-02. À lire avec `Claude.md` (spécification complète, décisions Q1-Q6 en section 13, notes d'implémentation en section 14).

## Où on en est

Toute la spec est implémentée et testée, plus quatre ajouts : autocomplétion, renommage, formatage et panneau Git. Tout est commité sur `main` et l'arbre de travail est propre.

| Commit | Contenu |
|---|---|
| `02ddfb7` | Pod Go + front SolidJS (toute la spec) |
| `79a71a0` | Éditeur découpé en blocs (perf gros fichiers) |
| `191dde6` | Tests e2e, tunnel SSH testé, service systemd |
| `e647ede` | Autocomplétion, renommage (Maj+F6), formatage (Ctrl+Alt+L) |
| `e336135` | Panneau Git, onglet de diff, marqueurs de gouttière |

## Commandes

```
make build          # front (Vite) puis binaire bin/web-ide-pod (front embarqué)
./bin/web-ide-pod   # http://127.0.0.1:4433/?token=… (jeton dans ~/.web-ide/token)
make dev            # pod -allow-remote sur 0.0.0.0:4433 + Vite 0.0.0.0:5173 (HMR)
make test           # go vet + go test + tsc
make e2e            # tests navigateur (toutes les suites, ~3 min)
./e2e/run.sh git    # une suite : editing features restore+ git lsp perf
make service        # service systemd utilisateur (pas activé à ce jour)
```

Go 1.27 est dans `~/sdk/go/bin`, pas dans le PATH ; le Makefile le trouve tout seul. gopls est dans `~/go/bin` ; le pod a besoin de `~/go/bin` **et** de `~/sdk/go/bin` dans son PATH pour la navigation Go (gopls appelle `go`).

Tests DB optionnels sur de vrais serveurs : `WEBIDE_TEST_PG=hôte:port:user:mdp WEBIDE_TEST_REDIS=hôte:port:mdp go test ./internal/db/` (validés avec des conteneurs Docker temporaires, supprimés depuis).

## Architecture en bref

- `pod/internal` : `server` (HTTP, pairing, RPC WebSocket, files séquentielles + barrière LSP), `runtime` (un par projet ouvert), `fsx` (local/SFTP), `sshx`, `execx` (processus locaux ou SSH), `console`, `lsp`, `db`, `git`, `search`, `sshtest` (faux serveur SSH pour les tests).
- `web/src` : `editor/` (Doc, EditorView en blocs, tokenizer + grammaires, merge, linediff, FindBar), `state/` (project, settings, git), `keys/` (presets JSON + bindings), `lsp/` (client, completion, edits, refactor), `ui/` (EditorArea, Completion, DiffView, overlays), `panels/`, `tools/`, `db/`, `console/`, `pages/`.
- `e2e/` : `run.sh` lance un pod neuf par suite (données et workspace temporaires copiés de `e2e/fixtures`), Chromium du cache Playwright (`~/.cache/ms-playwright`) ou `CHROME=…`.

## Pièges rencontrés (à ne pas refaire)

- **Solid vide un conteneur** dont l'unique enfant est dynamique (`textContent = ''`) : tout nœud monté à la main (la vue de l'éditeur) doit avoir son propre élément, voir `.editor-mount`.
- **Callbacks de la vue sous `untrack`** (`useEditorView`) : sinon un effet qui appelle `setSelection` s'abonne à ce que lit le callback (bug : le curseur revenait à la dernière cible de navigation).
- **Blocs de l'éditeur en `width: max-content`** : sinon allonger la ligne la plus longue relance la mise en page de tout le fichier (~50 ms par frappe sur 100 000 lignes).
- **Pas de `push(...grosTableau)`** (dépassement de pile sur les gros collages) : copier en boucle.
- **Une requête LSP doit partir après les `didChange`** : `flushLsp(path)` côté front + barrière côté pod (`server.go`, `barrier`).
- **Shell** : `pkill -f <motif>` tue aussi la commande qui le lance (le motif est dans sa ligne de commande) ; utiliser `pkill -x web-ide-pod`. Lancer le pod de test avec `setsid` / `< /dev/null`, sinon un pipe reste ouvert et la commande ne rend pas la main.
- **npm** : `typescript@latest` est TS 7 (binaires natifs), dont l'installation a bloqué ; le projet est sur TypeScript 5.
- Les tests e2e doivent attendre activement (`waitForFunction`) : gopls démarre à froid, l'index de la bibliothèque standard prend plusieurs secondes.

## Limites connues

- Navigation de code testée uniquement avec gopls ; PHP (intelephense / phpactor), Python (pyright) et TypeScript (typescript-language-server) non installés ici, donc jamais essayés.
- Testé sous Chromium seulement (Firefox non essayé).
- SSH validé avec le faux serveur des tests, pas sur un vrai hôte (le sshd local n'autorise pas la clé, `authorized_keys` non modifié volontairement).
- Recherche globale : regex en syntaxe RE2 (Go), pas de lookbehind.
- SQLite d'un projet SSH : via `sqlite3` en ligne de commande sur l'hôte, autocommit seulement.
- `make service` écrit le service mais ne l'active pas : c'est à l'utilisateur de le lancer.

## Prochaines étapes proposées

1. Complétion dans la console SQL (mots-clés, tables, colonnes de la connexion).
2. Recherche et remplacement dans tout le projet.
3. Essayer intelephense / pyright / typescript-language-server sur de vrais projets de `~/Apps`.

## Méthode de travail retenue

- Un commit par fonctionnalité, testée (Go + e2e) avant le commit ; messages en français, fin de message avec la ligne `Claude-Session`.
- Textes de l'interface et messages en français, commentaires du code en anglais.
- Ajouter une suite ou des assertions e2e à chaque fonctionnalité visible.
