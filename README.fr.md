# Web IDE

*[English version](README.md)*

Un IDE auto-hébergé dans votre navigateur, avec un agent d'IA qui tourne sur **vos** modèles (llama.cpp, Ollama) et un kanban qui mène chaque modification de l'idée à la branche fusionnée.

![Le modèle développe un ticket dans son propre worktree git : il lit le code, modifie, lance les tests et commite](docs/images/develop.gif)

- **Un binaire, local avant tout.** Un petit programme Go, le *pod*, sert l'interface et fait le travail sur votre machine : fichiers, git, terminaux, serveurs de langage, bases de données, appels au modèle. Pas d'Electron, pas de cloud, pas de compte.
- **La même session partout.** Ouvrez-le depuis un autre navigateur ou appareil (via Tailscale) : onglets, découpages et terminaux sont tels que vous les avez laissés.
- **Un cycle de développement avec l'agent.** Briefing → plan → développement dans un worktree → test → fusion, chaque étape avec sa conversation, chaque décision à vous.

## De l'idée à la branche fusionnée

| | |
|---|---|
| **1. Briefing** : décrivez le besoin ; le modèle lit le code, pose ses questions, puis écrit le ticket. <br><br> ![Briefing](docs/images/briefing.gif) | **2. Plan** : le modèle écrit le plan d'implémentation et des objectifs vérifiables. <br><br> ![Plan](docs/images/plan.png) |
| **3. Développement** : une branche et un worktree par ticket, dans sa propre fenêtre ; le modèle code, teste, commite. <br><br> ![Développement](docs/images/develop.png) | **4. Test et fusion** : comment tester, les objectifs, le diff ; les retours repartent au modèle ; fusion quand c'est bon. <br><br> ![Revue](docs/images/review.png) |

Le parcours complet est dans le **[guide d'utilisation](docs/guide.fr.md)**.

## Et un vrai éditeur

![L'éditeur avec la complétion de gopls, et un terminal](docs/images/editor.png)

- **Éditeur** : rapide sur des fichiers de 100 000 lignes, découpages, multi-curseur, repli, Search Everywhere, Recent Files, historique du presse-papier, thèmes (ici *High contrast*), raccourcis QWERTY et AZERTY, navigation complète au clavier.
- **Serveurs de langage** : définition, références, complétion, renommage, formatage, diagnostics (gopls, typescript-language-server, pyright, intelephense…).
- **Git** : modifications, index, commits, branches, graphe de l'historique, diffs côte à côte, worktrees.
- **Terminaux**, **bases de données** (SQLite, PostgreSQL, Redis), **Docker** (Compose, conteneurs, journaux) et tunnels SSH.
- **Projets locaux et SSH**, avec vos clés locales.
- **Sûr avec les autres outils** : les modifications faites par un autre programme aux fichiers ouverts sont fusionnées dans votre buffer.
- **Assistant** : modes Build, Plan et Briefing, `CLAUDE.md` / `AGENTS.md` et skills, diagrammes Mermaid, pièces jointes image / PDF / audio, dictée locale (Whisper dans le navigateur), croquis.
- Interface en **anglais et en français**.

| | |
|---|---|
| ![Le kanban](docs/images/kanban.png) | ![Le panneau Git](docs/images/git.png) |

## Installation

Linux, en service lancé au démarrage ([détails](docs/guide.fr.md#installation)) :

```sh
curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh
# depuis vos autres appareils, en HTTPS sur votre réseau Tailscale :
curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh -s -- --tailscale
```

Le script affiche l'adresse à ouvrir une fois, avec le jeton d'appairage. Sous macOS, téléchargez l'archive de la [dernière version](https://github.com/DrSmithFr/web-ide/releases/latest) et lancez `./web-ide-pod`.

Depuis les sources (Go 1.27+, Node.js 20.19+) :

```sh
git clone https://github.com/DrSmithFr/web-ide.git && cd web-ide
make build && ./bin/web-ide-pod
```

Facultatif : `git`, des serveurs de langage dans le `PATH`, un serveur [llama.cpp](https://github.com/ggml-org/llama.cpp) ou [Ollama](https://ollama.com) pour l'assistant.

## Sécurité

Le pod a les droits de votre utilisateur : il lit et écrit des fichiers et lance des commandes pour la page. Il ne répond qu'à la machine locale, chaque requête exige le jeton d'appairage, et l'agent ne doit être branché qu'à des serveurs de modèles de confiance. Voir [SECURITY.md](SECURITY.md).

## Contribuer

[CONTRIBUTING.md](CONTRIBUTING.md) pour démarrer, [docs/architecture.md](docs/architecture.md) pour le code, [docs/spec.md](docs/spec.md) pour le comportement attendu, [CHANGELOG.md](CHANGELOG.md) pour les versions.

## Licence

[MIT](LICENSE)
