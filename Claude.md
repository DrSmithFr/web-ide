# IDE web - spécifications

Document de reprise du brainstorm complet. Phase actuelle : première implémentation complète (pod Go dans `pod/`, front SolidJS dans `web/`), voir section 14.

## 1. Vue d'ensemble

IDE dans le navigateur, dans le même esprit que le studio VTubing/VR. Utilisable sans installation côté frontend, grâce à un petit agent local (le **pod**) qui parle en WebSocket avec la page web.

- Le navigateur seul ne peut pas ouvrir de socket TCP ni de connexion SSH.
- Le pod est un binaire unique (Go ou Rust, aucune dépendance runtime) qui tourne sur la machine de l'utilisateur.
- Le pod donne l'accès au disque local, à SSH (jeu de clés local) et aux bases de données.
- Les clés privées ne quittent jamais la machine et ne sont jamais envoyées à un backend distant.

## 2. Stack

- TypeScript sur toute l'application.
- **SolidJS** pour le chrome de l'IDE (menu, onglets, panneaux, réglages, statut, popups). Réactivité à granularité fine par signaux, sans VDOM.
- **Cœur de l'éditeur** (buffer, curseur, sélection, surlignage) : TypeScript pur, sans framework, DOM manipulé directement pour éviter la latence d'un diffing à chaque frappe.
- Tout l'état persistant est stocké côté pod dans `~/.web-ide` : réglages (historique de snapshots), registre des projets, sessions par projet. Le navigateur ne garde rien (décision du 2026-10-02, remplace IndexedDB).
- WASM : pas au départ. Commencer en JS pur, profiler ensuite. Candidats si goulot d'étranglement : le merge à trois voies (crates Rust `diffwtf_core` ou `diff-match-patch-rs`) et une future recherche multi-fichiers. Pas d'intérêt pour la tokenisation regex, la recherche dans un fichier ou la navigation par sous-mots.

## 3. Pod (agent local)

- Connexion WebSocket sur un port fixe et documenté (par exemple `127.0.0.1:4433`).
- Accès aux fichiers locaux et distants (SSH/SFTP), lecture/écriture/surveillance de fichiers.
- Streame les fichiers au front, pousse les nouvelles versions quand un fichier change (modification par l'IA).
- Porte la session de chaque projet (voir section 9).
- Héberge les connexions aux bases de données (voir section 11) et les serveurs de langage (voir section 12).
- Lecture de fichier générique par chemin absolu accessible au pod, pas limitée à la racine du projet (nécessaire pour les fichiers de dépendances et stubs, voir section 12).

Points techniques à traiter :

- **Mixed content** : une page HTTPS ne peut pas ouvrir `ws://localhost` sans blocage. Solutions : certificat local auto-signé accepté une fois, ou exception Chromium pour les adresses privées (Private Network Access, avec invite de permission).
- **Pairing** : sans contrôle, n'importe quel site ouvert dans le navigateur pourrait se connecter au pod. Jeton affiché une fois par le pod puis collé dans l'IDE, ou liste d'origines autorisées.
- **Persistance** : démarrage automatique ou icône de barre système, à décider selon usage ponctuel ou quotidien.

## 4. Accès aux fichiers depuis le navigateur

Rappel des possibilités et limites, pour le repli si besoin :

- File System Access API (`showDirectoryPicker()`, `showOpenFilePicker()`) : Chromium uniquement, permission d'écriture demandée par session.
- Glisser-déposer : `DataTransferItem.getAsFileSystemHandle()` dans Chromium.
- `<input type="file" webkitdirectory>` : partout, lecture seule, sans persistance.
- OPFS : stockage sandboxé, invisible pour l'utilisateur, utile en cache seulement.
- Aucune de ces API ne donne de socket réseau brut.

Pour unifier local et SSH, le pod expose une interface commune (`list`, `read`, `write`, `watch`) pour que l'éditeur traite fichiers locaux et distants de la même façon.

## 5. Disposition de l'interface

```
+--------------------------------------------------+
| Barre de menu (réglages)      statut pod / débit |
+----+-----------+-----------------------+---+-----+
| ic | Explorat. |  Onglets              | T | ic  |
| ôn |           |  Éditeur              | o |  ôn |
| es |           |                       | o |  es |
|    |           |                       | l |     |
+----+-----------+-----------------------+---+-----+
| Consoles (onglets)                               |
+--------------------------------------------------+
```

- **Barre de menu** en haut : accès aux réglages à gauche, statut du pod à droite (état de connexion, débit descendant et montant en o/s, calculé sur une fenêtre glissante d'environ une seconde pour lisser les pics).
- **Fenêtre d'éditeur** : panneau explorateur à gauche, éditeur central avec barre d'onglets, panneau tools à droite.
- **Rails d'icônes** à gauche et à droite : chaque icône bascule entre plusieurs panneaux. Un clic sur l'icône déjà active masque le panneau.
  - Gauche : explorateur de fichiers, recherche globale, connexions (local/SSH).
  - Droite : tools (propriétés, extensions, conflits, database explorer, etc.).
- **Panneau du bas** : consoles, avec plusieurs onglets (terminal, sortie de build, diagnostics).
- **Split de vue** : arbre de panneaux récursif façon VS Code (`split/right`, `split/down`). Un même fichier ouvert dans plusieurs panneaux partage le même buffer, jamais deux copies.

## 6. Éditeur

### 6.1 Colorisation syntaxique

- Langage détecté par extension en priorité, puis par contenu en repli (shebang, `<?php`, `package main`, blocs `server {`).
- Minimum : PHP, JS, TS, Python, Go, configuration nginx.
- Une grammaire par module : liste ordonnée de règles `{type de token, regex}`, la première qui matche à la position courante gagne. Ajouter un langage ne touche pas au moteur.
- Rendu avec la **CSS Custom Highlight API** (`CSS.highlights`, `::highlight()`), baseline depuis mars 2026. Un `Highlight` par type de token, pas de `<span>` dans le DOM, pas de saut de curseur.
- Limites de l'API : seules `color`, `background-color`, `text-decoration`, `text-shadow`, `font-style`, `caret-color`, `text-emphasis-color` sont autorisées. Pas de `font-weight` (donc pas de gras), l'italique reste possible.
- Retokeniser invalide les `Range`. Prévoir un debounce ou une retokenisation incrémentale (ligne modifiée seulement) pour les gros fichiers.

### 6.2 Recherche dans le fichier ouvert

- Barre flottante en haut à droite de l'éditeur, affichée/masquée par `F3`.
- Champ de requête, trois options : `Aa` (respecter la casse), `ab|` (mot entier, `\b...\b`), `.*` (mode regex).
- Compteur `n / N`, boutons précédent/suivant avec retour au début après le dernier résultat, bouton de fermeture.
- Mode texte : requête échappée puis compilée en regex globale (flag `i` si `Aa` est désactivé). Mode regex : requête utilisée telle quelle. Un motif invalide affiche une erreur à la place du compteur (`try/catch` autour du `RegExp`).
- Rendu avec la même API : `Highlight` `search-match` pour toutes les occurrences, `search-current` de priorité supérieure pour l'occurrence courante. Navigation sur le tableau d'offsets déjà calculé, sans recalcul.
- Aucun résultat : boutons de navigation désactivés.

### 6.3 Raccourcis de navigation

- `Ctrl+Flèche` : comportement natif du navigateur, ne pas intercepter (segmentation ICU, légères différences Chrome/Firefox).
- `Alt+Flèche` : saut entre frontières de casse dans un mot. Comportement maison, identique partout.
- `Shift+Alt+Flèche` : même logique, étend la sélection (ancre fixe, extrémité active déplacée).
- Frontières reconnues : `foo|Bar`, `XML|Http|Request` (avant le dernier capitale d'une suite suivie d'une minuscule), de part et d'autre de `_` et `-`, entre lettres et chiffres (`item|2`).
- `Ctrl+Alt+R` : ouvre la popup de résolution de conflit du fichier courant. Sans effet si le fichier n'est pas en conflit.

## 7. Raccourcis clavier (système)

- Table de bindings **centralisée** : action (`search.find`, `conflict.resolve`, `view.splitRight`, ...) associée à une combinaison, stockée par `code` (position physique) et non par `key`.
- Presets par défaut **QWERTY** et **AZERTY** au minimum (deux fichiers de données), le reste laissé à l'utilisateur.
- Détection de la disposition : `navigator.keyboard.getLayoutMap()` si disponible (Chromium), sinon réglage manuel avec `navigator.language` comme simple suggestion.
- Personnalisations stockées séparément comme liste de dérogations (`action -> binding`) par-dessus le preset. Réinitialisation possible par action.
- À l'assignation, vérifier les collisions et demander confirmation avant d'écraser.
- Menus et éventuelle palette de commandes reflètent les raccourcis remappés sans redémarrage.

## 8. Synchronisation et conflits (fichiers modifiés par l'IA)

- L'éditeur travaille sur une copie du fichier en mémoire, jamais directement sur le flux du pod.
- Trois versions par fichier ouvert :
  - `base` : contenu au chargement.
  - `local` : buffer courant, avec les modifications en cours.
  - `remote` : nouvelle version poussée par le pod (typiquement après modification par l'IA), avec numéro de révision.
- À chaque `remote` reçue : **merge à trois voies (diff3)**, ligne à ligne, avec une bibliothèque type `node-diff3`. Pas de diff d'AST.
- **Fusion propre** : application silencieuse, `base` devient `remote`, toast discret.
- **Conflit** : le fichier passe en état de conflit, le buffer local n'est pas écrasé tant que l'utilisateur n'a pas tranché.
- Interface de conflit :
  - Bandeau d'avertissement en haut de l'onglet, et marquage de l'onglet (icône ou couleur).
  - Tool **Conflits** listant tous les fichiers en conflit, avec navigation directe vers le fichier ou la popup.
  - Popup de résolution à trois volets façon PhpStorm : modification en cours (gauche), résultat éditable (centre), nouvelle version (droite). Boutons par bloc (accepter gauche / droite / les deux) et flèches dans la gouttière.
- Représentation textuelle optionnelle des conflits (`<<<<<<<` / `=======` / `>>>>>>>`) pour export ou repli.

## 9. Session et persistance

**Côté pod (contexte actif, par projet)** : fichiers ouverts, position du curseur par fichier, consoles ouvertes, disposition des splits (arbre, tailles, fichier actif par panneau). Poussé via WebSocket à chaque changement (debounce sur le curseur), restauré depuis le pod au chargement ou à la reconnexion. La session est accessible à l'identique depuis un autre navigateur ou une autre machine.

**Réglages (pod, `~/.web-ide/settings.json`)** : thèmes, polices, raccourcis, règles de colorisation personnalisées. Identiques d'un navigateur à l'autre.

- Historique par snapshot complet à chaque modification significative, dans `settings.json`. Entrées jamais modifiées.
- Pointeur `current` vers l'entrée active.
- Un rollback crée une nouvelle entrée qui copie l'ancienne, ce qui permet d'annuler un rollback.
- Nombre d'entrées plafonné (purge des plus anciennes).
- Snapshot forcé avant un import de règles de colorisation.
- Les réglages étant dans le pod, tous les navigateurs connectés au même pod les partagent.

## 10. Réglages, projets et fenêtres déportées

### Menu de réglages

Pop-in large (overlay modal), navigation à gauche et contenu à droite. Sections :

- Thèmes.
- Polices.
- Raccourcis clavier.
- Colorisation syntaxique : ajouter, modifier, exporter (JSON) les règles par langage.

### Page d'accueil et projets

- Liste simple des projets. Création d'un projet **local** ou **SSH**, titre et description facultatifs (nom dérivé du chemin ou de l'hôte si titre vide).
- Registre des projets (titre, description, type, cible) stocké dans le pod. La connexion elle-même (locale ou SSH) passe par le pod à l'ouverture.
- Chaque projet a son URL : `/project/:id`, contexte scopé au projet, session pod distincte par projet.
- Dossier `.ide` dans chaque projet : paramètres du projet, index, paramètres de connexion aux bases.

### Fenêtres déportées

Chaque panneau a un identifiant. Routes dédiées pour ouvrir un panneau seul, sans le reste du chrome :

- `/project/:id/editor`
- `/project/:id/console/:consoleId`
- `/project/:id/tool/:toolId`

Chaque fenêtre est un client WebSocket de plus sur la même session pod. Pas de communication inter-fenêtres à prévoir, le buffer partagé règle la synchronisation.

## 11. Tool Database explorer

Tool du panneau de droite. Connexions SQLite, Postgres et Redis, interface proche de PhpStorm. Toute la connectivité passe par le **pod** (un navigateur ne peut pas ouvrir de socket TCP). SQLite pourrait tourner en WASM dans le navigateur, mais ce n'est pas le modèle retenu.

### Arborescence

- Connexions > bases de données > tables > colonnes (nom + type) et index.
- Redis ne rentre pas dans ce modèle : pas de tables, mais des clés typées (`string`, `hash`, `list`, `set`, `zset`, `stream`) avec TTL.
- Comportement de l'explorateur de fichiers : expansion au clic, menu contextuel au clic droit.
- Menu contextuel : connexion (modifier, dupliquer, supprimer, rafraîchir), table (voir les données, voir le DDL, requête vide sur la table), colonne (copier le nom, copier le nom qualifié `table.colonne`), index (voir la définition).
- Double-clic sur une table : ouvre la vue tableur.
- Indicateur d'état par connexion : connectée, en erreur, non testée.

### Barre de menu du tool

- Ajouter une connexion.
- Ouvrir le panneau d'édition des connexions.
- Rafraîchir la connexion sélectionnée.
- Fermer la connexion sélectionnée.
- Ouvrir une console SQL (connexion sélectionnée).
- Ouvrir la table sélectionnée en vue tableur.

Les actions dépendant d'une sélection sont désactivées sans sélection. La console SQL et la vue tableur s'ouvrent comme des **onglets de l'éditeur principal**. Fermer une connexion laisse les consoles et vues tableur ouvertes **en mode déconnecté** : contenu affiché, nouvelle exécution en échec tant que la connexion n'est pas rouverte.

### Connexions

- SQLite : chemin du fichier (local ou via SSH).
- Postgres : hôte, port, base, utilisateur, mot de passe, mode SSL.
- Redis : hôte, port, base (index numérique), mot de passe si besoin.
- Bouton **Tester** dans le formulaire d'ajout.
- Mot de passe : au choix mémorisé côté pod ou redemandé à chaque ouverture (invite de saisie valable pour la session).
- **Tunnel SSH** optionnel par connexion (Postgres et Redis) : hôte et port SSH, utilisateur, authentification mot de passe ou clé, puis hôte et port de la base vus depuis le serveur (typiquement une IP locale). Pour la clé : réutiliser le jeu de clés existant ou en définir un distinct par connexion. Le pod ouvre le tunnel puis la connexion à la base à travers.
- Paramètres de connexion stockés dans le dossier `.ide` du projet. La page web ne reçoit que les métadonnées non sensibles (nom, type, hôte).

### Console SQL

- Colorisation selon le langage de requête (SQL pour Postgres/SQLite, commandes Redis pour Redis).
- Plusieurs requêtes séparées par `;`. La requête active (celle du curseur) est encadrée.
- **Exécuter la requête active** : s'il y a plusieurs requêtes, ouvre une petite pop-in navigable au clavier, présélectionnée sur la requête active, où l'on choisit la requête à exécuter. Une seule requête : exécution directe.
- Raccourci d'exécution : `Ctrl+Entrée`. Résultat en grille sous l'éditeur, avec temps d'exécution et nombre de lignes affectées.
- Barre de menu : exécuter la requête active, afficher l'historique (pop-in avec liste des commandes à gauche et vue détaillée à droite), transaction automatique on/off, commit, rollback, annuler les instructions en cours.
- Transaction automatique désactivée : chaque instruction reste en attente d'un commit ou rollback. Commit et rollback sont désactivés en mode automatique ou sans transaction ouverte.
- Redis : la console accepte les commandes natives (`GET`, `HGETALL`, `SCAN`).

### Vue tableur

Contenu paginé de la table sélectionnée, sans requête à écrire. Même connexion que la console SQL, cache de résultats partagé.

## 12. Navigation dans le code (LSP)

Objectif : reproduire le goto de PhpStorm (déclaration/usages, implémentations, déclaration de type, super méthode, symboles liés, tests).

- PhpStorm utilise PSI : indexation incrémentale en arrière-plan (stub trees, index de mots, index de hiérarchie de types, cache de résolution), puis résolution à la demande. Trop lourd à reproduire.
- Approche retenue : **LSP** (Language Server Protocol, JSON-RPC). Le pod lance les serveurs, l'éditeur web parle LSP via WebSocket.
- Serveurs envisagés : `gopls` (Go), `intelephense` ou `phpactor` (PHP), `pyright` (Python), `typescript-language-server` (JS/TS).
- Requêtes utilisées : `textDocument/definition`, `references`, `implementation`, `typeDefinition`, `documentSymbol`, `workspace/symbol`, plus `textDocument/didChange` pour envoyer les modifications.
- Hors LSP, à construire soi-même : super méthode, symboles liés, tests (par convention de nommage ou configuration par projet).
- Ajouts au-delà du brainstorm initial : autocomplétion (`textDocument/completion`, Ctrl+Espace, ouverture automatique sur les caractères déclencheurs du serveur ou après une pause dans un identifiant, filtrage approximatif côté client, snippets réduits à du texte, imports automatiques par `additionalTextEdits` annulables en un seul undo, mots du fichier si aucun serveur), renommage (Maj+F6, `prepareRename` puis `rename` sur tout le projet : buffers ouverts modifiés, autres fichiers écrits directement), formatage (Ctrl+Alt+L, document ou sélection). Le pod garantit qu'une requête LSP part après les `didChange` reçus avant elle.
- **Un serveur par projet et par langage présent**, avec la racine du projet comme `rootUri`. Le pod détecte les langages (`composer.json`, `package.json`, `go.mod`, ...), multiplexe selon l'extension du fichier, et arrête les serveurs quand il n'y a plus de session active (certains consomment plusieurs centaines de Mo).
- Multi-root possible via `workspace/didChangeWorkspaceFolders` pour certains serveurs, toujours une instance par langage.

### Git (ajout)

- Panneau Git dans le rail de gauche : branche (changement, création), avance/retard sur l'amont, message de commit (Ctrl+Entrée, amend), sections Conflits / Indexés / Modifications avec indexer, désindexer, annuler (non suivis supprimés), historique des 30 derniers commits.
- Pull, push et fetch dans un terminal du panneau du bas (les demandes d'identifiants restent interactives).
- Onglet de diff côte à côte : copie de travail ↔ index, ou index ↔ HEAD ; parties identiques repliées, navigation entre modifications ; le côté travail suit le buffer ouvert.
- Gouttière : lignes ajoutées, modifiées, supprimées par rapport à HEAD, recalculées pendant la frappe. Explorateur : fichiers et dossiers modifiés colorés.
- Côté pod : `git` lancé par le même exécuteur que le reste, donc aussi sur un hôte SSH ; le projet peut être un sous-dossier du dépôt.

### Accès aux sources hors projet

Les définitions peuvent pointer hors du projet (`lib.es5.d.ts` dans `node_modules/typescript/lib`, stubs PHP, `GOROOT/src`, `.pyi` bundlés avec pyright).

- Le pod lit n'importe quel chemin absolu accessible.
- Onglets de ces fichiers en **lecture seule**, absents de l'explorateur tant qu'ils ne sont pas ouverts par navigation.
- Projet SSH : la résolution de chemin se fait sur l'hôte distant.
- Prérequis : TypeScript installé comme dépendance du projet, stubs PHP disponibles dans l'environnement du pod, installation Go avec sources.
- Point d'extension à prévoir pour les schémas d'URI autres que `file://` (par exemple `deno:` et `deno/virtualTextDocument`), seulement si Deno est supporté plus tard.

## 13. Questions tranchées

- **Q1** : le pod pousse le fichier complet (`fs.changed`) à chaque changement, après 150 ms de stabilisation (les outils d'IA écrivent souvent en plusieurs fois).
- **Q2** : « accepter les deux » concatène local puis distant.
- **Q3** : les identifiants ne sont jamais écrits dans `.ide`. Mot de passe mémorisé : `~/.web-ide/secrets.json` (0600, même modèle que `~/.pgpass`, non chiffré) ; sinon gardé en mémoire du pod pour la session.
- **Q4** : nœud dédié aux clés Redis (type et TTL en détail), sous chaque base `dbN`.
- **Q5** : service utilisateur systemd (`make service` : binaire dans `~/.local/bin`, démarrage avec la session, redémarrage en cas d'échec) ; `make run` pour un lancement ponctuel. Pas d'icône de barre système.
- **Q6** : pod local (clés SSH locales), écoute sur `127.0.0.1` et refuse les autres machines. `-allow-remote` permet l'accès distant protégé par le jeton seul.

## 14. Implémentation

Décisions du 2026-10-02 : pod en Go, état dans `~/.web-ide`, workspace par défaut `~/Apps`, front embarqué dans le binaire (même origine : pas de mixed content, pairing par cookie).

- **Lancer** : `make build && ./bin/web-ide-pod`, puis ouvrir l'URL affichée (`http://127.0.0.1:4433/?token=…`). Options : `-addr`, `-workspace`, `-data`, `-allow-remote`, `-static`.
- **Développement** : `make dev` lance le pod avec `-allow-remote` sur `0.0.0.0:4433` (modifiable par `DEV_ADDR`) et Vite sur `0.0.0.0:5173` (proxy `/ws`, `/auth` et les liens `?token=`). Ouvrir `http://<hôte>:5173/?token=…` depuis n'importe quelle machine ; le jeton est alors la seule protection. Le binaire hors dev reste limité à la machine locale par défaut.
- **Tests navigateur** : `make e2e` (`e2e/run.sh [suite…]`, Chromium sans fenêtre du cache Playwright ou `CHROME=…`). Un pod neuf par suite, avec données et workspace temporaires copiés de `e2e/fixtures` : `editing`, `features`, `restore` (enchaînée sur la précédente), `lsp` (si gopls est installé), `perf` (100 000 lignes, médiane < 50 ms par frappe, réglable par `E2E_PERF_MS`).
- **Tests** : `make test` (tests Go, dont un test d'intégration WebSocket de bout en bout et un serveur SSH en mémoire pour les projets distants, puis `tsc`). Pilotes Postgres et Redis sur de vrais serveurs, sur demande : `WEBIDE_TEST_PG=hôte:port:user:mdp WEBIDE_TEST_REDIS=hôte:port:mdp go test ./internal/db/`.
- **`~/.web-ide`** : `config.json` (adresse, workspace), `token`, `projects.json`, `settings.json`, `sessions/<projet>.json`, `secrets.json`, `known_hosts` (TOFU, en plus de `~/.ssh/known_hosts`), `sql-history/`.
- **`.ide/` du projet** : `connections.json` (connexions BDD sans secret), `project.json` (`lsp` : commande par langage, `tests` : motif par extension, ex. `{".php": "{name}Spec.php"}`).
- **Protocole** : JSON sur WebSocket. Requête `{id, method, params}` → `{id, result | error}` ; événements poussés `{event, data}` (`fs.changed`, `fs.dir`, `buffer.synced`, `session.changed`, `console.output`, `lsp.diagnostics`, `db.changed`…). `id: 0` = notification sans réponse.
- **Pod** (`pod/internal`) : `server` (HTTP, pairing, RPC), `runtime` (un par projet ouvert : FS, watch, révisions, buffers partagés), `fsx` (local + SFTP, watch fsnotify ou polling), `sshx` (agent, clés, mot de passe, TOFU, pool), `execx` (processus locaux ou SSH), `console` (PTY + scrollback), `lsp`, `db` (SQLite modernc, Postgres pgconn, Redis go-redis, tunnel SSH), `search`.
- **Front** (`web/src`) : `editor/` (Doc, EditorView, tokenizer, grammaires, merge, sous-mots, FindBar), `state/` (projet/session, réglages), `keys/` (table de raccourcis + presets JSON), `ui/`, `panels/`, `tools/`, `db/`, `console/`, `conflict/`, `settings/`, `pages/`.
- **Limites connues** : regex de recherche globale en syntaxe RE2 (Go), pas de lookbehind ; SQLite d'un projet SSH via `sqlite3` en ligne de commande sur l'hôte (autocommit seulement) ; largeur des caractères supposée monospace pour le défilement horizontal.
