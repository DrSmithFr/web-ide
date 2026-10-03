# Kanban par projet · conception

Décisions prises avec l'utilisateur le 2026-10-03. Le kanban est un outil de l'IDE, source d'interaction entre l'utilisateur et les agents (assistant IA).

## Stockage

- Base SQLite non versionnée : `<projet>/.ide/kanban.db` pour un projet local, `~/.web-ide/kanban/<projet>.db` pour un projet SSH (comme `chats.db`). `.ide/.gitignore` exclut `kanban.db*` et `worktrees/`.
- Pièces jointes dans la base (table `attachments`, 20 Mo max par fichier).
- Un worktree de ticket ouvert comme projet utilise la base (kanban **et** conversations) de son projet parent.

## Ticket

Numéro (#1, #2… par projet), titre, type (Fonctionnalité, Bug, Refacto, Tâche), priorité (Basse, Normale, Haute, Critique), description (Markdown), fichiers liés (simples chemins), pièces jointes, notes, plan (Markdown), goals (objectifs vérifiables à cocher), résumé de test, conversations liées (avec leur rôle), branche, base de comparaison, worktree, commits liés, historique (événements).

## États et transitions (boutons seulement, pas de glisser-déposer)

| État | Boutons de l'utilisateur | Le modèle peut |
|---|---|---|
| Nouveau | Briefing, Générer le plan, Passer à développer (si plan), Abandonner | passer à « À développer » (après avoir écrit le plan et les goals) |
| À développer | Lancer une session de dev (→ En cours), Abandonner | — |
| En cours | Nouvelle session, Envoyer en test, Abandonner | passer à « À tester » (avec le résumé de test) |
| À tester | Ajouter un retour (→ Correction), Fermer (→ Terminé), Abandonner | — |
| Correction | Nouvelle session de correction, Envoyer en test, Abandonner | passer à « À tester » |
| Terminé / Abandonné | Rouvrir | — |

Les retours de test deviennent des goals (source `feedback`). Fermer ou abandonner supprime le worktree ; la branche est gardée (proposée à la suppression pour l'abandon). À la fermeture, la liste des fichiers et le diff sont figés dans le ticket.

## Conversations liées

Champ `chat.ticket = { id, role }` ; rôles : `briefing` (mode Plan, Nouveau), `plan` (mode Plan, génère plan + goals), `dev` (Build, dans le worktree), `correction` (Build, dans le worktree, retours en contexte), `resolve` (résolution de conflits de rebase). Le prompt système reçoit le ticket à jour (description, notes, plan, goals, retours) et des consignes selon le rôle.

## Outils de l'agent

- Dans toutes les conversations : `kanban_list`, `kanban_get`, `kanban_create` (un nouveau ticket), `ask_user` (1 à 10 questions à choix + réponse libre, carte dans le fil, une question à la fois).
- Seulement dans une conversation liée à un ticket, et seulement sur ce ticket : `kanban_update` (titre, description, type, priorité, fichiers), `kanban_add_note`, `kanban_set_plan` (plan + goals), `kanban_goal` (cocher / ajouter), `kanban_move` (transitions permises au modèle ; `À tester` exige un résumé de test), `kanban_link_commit`.

## Git

- Au passage en « En cours » : `git fetch` (si remote), puis branche `ticket/<n>-<slug>` créée depuis la base (défaut `origin/main`, sinon `main` local, modifiable par ticket) dans un worktree `<projet>/.ide/worktrees/<n>-<slug>`.
- Commande d'initialisation du worktree configurable (`npm install && cp ../.env .`…), lancée à la création.
- Le worktree s'ouvre comme un projet à part (option a) : non listé sur l'accueil, ouvert depuis son ticket, bandeau « Ticket #n » en haut.
- Le modèle gère la branche et les commits (messages préfixés `#n `) ; l'utilisateur déclenche la fusion (`merge --no-ff` par défaut ou `squash`, dans la branche de base locale du dossier principal, jamais de push, refusée si le dossier principal a des modifications suivies non commitées) et le rebase.
- Conflits : pas d'abort automatique ; le ticket montre les fichiers en conflit avec Continuer / Abandonner / Lancer une session de résolution.
- Sur « En cours », « À tester », « Correction » et « Terminé » : fichiers affectés et diff par rapport à la base choisie (merge-base) + modifications non commitées du worktree.

## Interface

- Tableau en onglet de l'éditeur (une colonne par état, Terminé et Abandonné repliables), détail d'un ticket en onglet, liste compacte dans un panneau latéral. Synchronisé entre fenêtres (événement `kanban.changed`).
