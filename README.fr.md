# Web IDE

*[English version](README.md)*

Un IDE auto-hébergé qui tourne dans le navigateur, adossé à un petit agent local — le **pod** — qui lui donne ce qu'une page web ne peut pas avoir : votre disque, SSH, des terminaux, les serveurs de langage, les bases de données, git et des modèles d'IA locaux.

- **Un seul binaire.** Le pod est un exécutable Go qui embarque l'application web. Pas d'Electron, pas de cloud, pas de compte.
- **Local avant tout.** Tout (réglages, projets, sessions, conversations) est stocké par le pod dans `~/.web-ide` et dans le dossier `.ide` du projet. Les clés privées et l'audio ne quittent jamais votre machine.
- **La même session partout.** Ouvrez l'IDE depuis un autre navigateur ou une autre machine reliée au même pod : onglets, découpages, curseurs et terminaux sont tels que vous les avez laissés.

## Fonctionnalités

- **Éditeur** : rendu par blocs rapide (fichiers de 100 000 lignes), coloration syntaxique par la CSS Custom Highlight API (Go, PHP, JavaScript, TypeScript, Python, nginx…), panneaux divisés partageant les buffers, recherche dans le fichier et dans tout le projet, navigation par sous-mots, raccourcis QWERTY et AZERTY, thèmes.
- **Sûr avec les autres outils** : quand un agent d'IA ou un autre programme modifie un fichier ouvert, la modification est fusionnée dans votre buffer (fusion à trois voies) ; les vrais conflits ouvrent une fenêtre de résolution à trois volets.
- **Navigation dans le code** par les serveurs de langage (gopls, intelephense, pyright, typescript-language-server…) : définition, références, implémentations, symboles, complétion, renommage, formatage, diagnostics.
- **Projets locaux et SSH** : les mêmes fonctions sur un hôte distant en SSH/SFTP, avec vos clés locales.
- **Terminaux et commandes** dans le panneau du bas, détachables dans leur propre fenêtre.
- **Git** : état, index, commits, branches, historique, diffs côte à côte, marqueurs de gouttière.
- **Explorateur de bases de données** : SQLite, PostgreSQL et Redis, console SQL avec transactions, vue tableur, tunnels SSH.
- **Assistant IA** pour serveurs llama.cpp et Ollama : un agent qui lit, cherche et modifie le projet, utilise les serveurs de langage, lance des commandes, vous pose des questions, suit les instructions `CLAUDE.md` / `AGENTS.md` et les skills, avec modes Plan / Build, compaction automatique du contexte, diagrammes Mermaid, pièces jointes image / PDF / audio et dictée locale (Whisper dans le navigateur).
- **Kanban par projet** : les tickets passent du briefing au plan, au développement et au test avec des conversations liées ; chaque ticket en développement a sa branche et son worktree git, ouverts dans leur propre fenêtre, avec diff, fusion et rebase. Voir [docs/kanban.md](docs/kanban.md).
- Interface en **anglais et en français**.

## Prérequis

- Linux ou macOS (développé sous Linux).
- Pour compiler : Go 1.27+, Node.js 20.19+ et npm.
- Facultatif : `git` ; des serveurs de langage dans le `PATH` du pod (`gopls`, `intelephense`, `pyright`, `typescript-language-server`) ; un serveur [llama.cpp](https://github.com/ggml-org/llama.cpp) ou [Ollama](https://ollama.com) pour l'assistant.

## Démarrage rapide

```sh
git clone https://github.com/DrSmithFr/web-ide.git
cd web-ide
make build
./bin/web-ide-pod
```

Le pod affiche une adresse comme `http://127.0.0.1:4433/?token=…`. Ouvrez-la une fois : le jeton est gardé dans un cookie et appaire le navigateur avec le pod (il est aussi dans `~/.web-ide/token`).

Options : `-addr` (adresse d'écoute), `-workspace` (dossier par défaut des nouveaux projets, `~/Apps`), `-data` (dossier des données, `~/.web-ide`), `-allow-remote` (accepter d'autres machines), `-static` (servir le front depuis un dossier).

Pour lancer le pod avec votre session, installez-le comme service utilisateur systemd :

```sh
make service   # binaire dans ~/.local/bin, service web-ide-pod
journalctl --user -u web-ide-pod   # affiche l'adresse avec le jeton
```

## Sécurité

Le pod a les droits de l'utilisateur qui le lance : il lit et écrit des fichiers, lance des commandes et ouvre des connexions SSH pour vous. Par défaut il n'accepte que la machine locale, et chaque requête exige le jeton d'appairage. Avec `-allow-remote`, le jeton est la seule protection : à réserver à un réseau de confiance, ou derrière un proxy TLS. Voir [SECURITY.md](SECURITY.md).

## Développement

```sh
make dev    # pod sur :4433 (avec -allow-remote) + serveur Vite avec rechargement à chaud sur :5173
make test   # go vet, tests Go, vérification TypeScript
make e2e    # tests navigateur (Chromium sans fenêtre du cache Playwright, ou CHROME=/chemin/vers/chrome)
```

Lire [docs/architecture.md](docs/architecture.md) pour l'organisation du code et [CONTRIBUTING.md](CONTRIBUTING.md) avant de proposer une pull request.

## Licence

[MIT](LICENSE)
