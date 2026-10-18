package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// The team side of the panel (#88). The admin of a person's team is that person; the admin of an
// organization's team is proven once: the panel opens GitHub again asking read:org (for this
// alone), the relay reads the membership of the person in each organization that sponsors at the
// Team tier with that token, keeps admin → organization, and drops the token like any other.

const purposeTeam = "team"

// TeamInfo is a team as the panel of its admin sees it.
type TeamInfo struct {
	Owner   string       `json:"owner"` // the GitHub id of the owner
	Name    string       `json:"name"`
	Org     bool         `json:"org,omitempty"`
	Seats   int          `json:"seats"`
	Members []TeamMember `json:"members"`
}

// MarshalJSON of a member: the fields the panel shows.
func (m TeamMember) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Login string `json:"login,omitempty"`
	}{m.Kind, m.ID, m.Login})
}

// orgMembership reads the membership of the person a token belongs to in an organization:
// admin when active with the admin role; restricted when the organization refuses the OAuth app
// (its third-party access policy answers 403).
func (g *GitHub) orgMembership(ctx context.Context, token, org string) (admin, restricted bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiBase+"/user/memberships/orgs/"+url.PathEscape(org), nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := g.client.Do(req)
	if err != nil {
		return false, false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return false, true, nil
	case http.StatusNotFound:
		return false, false, nil
	default:
		return false, false, fmt.Errorf("github: memberships of %s answered %d", org, res.StatusCode)
	}
	var out struct {
		State string `json:"state"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return false, false, err
	}
	return out.State == "active" && out.Role == "admin", false, nil
}

// teamDoneWords are the lines of the window after the team check, in the language of the browser.
type teamDoneWords struct{ title, admin, restricted, none, otherLogin, close string }

var teamDoneCatalog = map[string]teamDoneWords{
	"en": {"Team", "You manage the team %s: add its members in Settings › Remote access › Members.",
		"%s restricts the apps it allows: an owner of the organization allows this relay in its settings (Third-party access), or asks the maintainer of the relay.",
		"This GitHub account is not an admin of an organization sponsoring Web IDE at the Team tier.",
		"This GitHub account (%s) is not the one linked to this account: link it first.", "Close"},
	"fr": {"Équipe", "Vous gérez l’équipe %s : ajoutez ses membres dans Réglages › Accès distant › Membres.",
		"%s restreint les applications qu’elle autorise : un propriétaire de l’organisation autorise ce relay dans ses réglages (Third-party access), ou demande au mainteneur du relay.",
		"Ce compte GitHub n’est admin d’aucune organisation qui soutient Web IDE au palier Team.",
		"Ce compte GitHub (%s) n’est pas celui lié à ce compte : liez-le d’abord.", "Fermer"},
	"es": {"Equipo", "Gestionas el equipo %s: añade sus miembros en Ajustes › Acceso remoto › Miembros.",
		"%s restringe las aplicaciones que permite: un propietario de la organización autoriza este relay en sus ajustes (Third-party access), o pide al mantenedor del relay.",
		"Esta cuenta de GitHub no es admin de ninguna organización que patrocine Web IDE en el nivel Team.",
		"Esta cuenta de GitHub (%s) no es la vinculada a esta cuenta: vincúlala primero.", "Cerrar"},
}

// serveTeamDone is the end of the team flow: the person must be the one linked to the account;
// each organization sponsoring at the Team tier is asked whether they are its admin.
func (s *Server) serveTeamDone(ctx context.Context, w http.ResponseWriter, r *http.Request, p *githubPending, token, id, login string) {
	lang := i18n.Detect(r.Header.Get("Accept-Language"))
	words, ok := teamDoneCatalog[lang]
	if !ok {
		lang, words = "en", teamDoneCatalog["en"]
	}
	linked, _, _ := s.store.GitHubLink(p.account)
	if linked != id {
		writeDonePage(w, lang, words.title, "", "", fmt.Sprintf(words.otherLogin, "@"+login), "", words.close)
		return
	}
	var admins, restricted []string
	sponsors, _ := s.store.sponsors()
	for _, sp := range sponsors {
		if !sp.Org || seats(sp) == 0 {
			continue
		}
		admin, closed, err := s.GitHub.orgMembership(ctx, token, sp.Login)
		switch {
		case err != nil:
			log.Printf("relay: github team: %v", err)
		case closed:
			restricted = append(restricted, sp.Login)
		case admin:
			if err := s.store.PutTeam(Team{OwnerID: sp.ID, Org: true, Name: sp.Login, AdminID: id}); err != nil {
				log.Printf("relay: github team: %v", err)
				continue
			}
			admins = append(admins, sp.Login)
		}
	}
	line := words.none
	if len(admins) > 0 {
		line = fmt.Sprintf(words.admin, strings.Join(admins, ", "))
	}
	muted := ""
	if len(restricted) > 0 {
		muted = fmt.Sprintf(words.restricted, strings.Join(restricted, ", "))
	}
	writeDonePage(w, lang, words.title, id, login, line, muted, words.close)
}

// adminTeams are the teams the account manages: through its linked GitHub id, the team of its own
// sponsorship (made on first use) and the organizations it was proven the admin of. mayOwn tells
// the panel to offer the team flow: an organization sponsors at the Team tier.
func (s *Server) adminTeams(account string) (teams []TeamInfo, mayOwn bool, err error) {
	githubID, login, err := s.store.GitHubLink(account)
	if err != nil || githubID == "" {
		return nil, false, err
	}
	if n := s.store.TeamSeats(githubID); n > 0 {
		if t, _ := s.store.Team(githubID); t == nil {
			if err := s.store.PutTeam(Team{OwnerID: githubID, Name: login, AdminID: githubID}); err != nil {
				return nil, false, err
			}
		}
	}
	sponsors, _ := s.store.sponsors()
	for _, sp := range sponsors {
		mayOwn = mayOwn || (sp.Org && seats(sp) > 0)
	}
	list, err := s.store.Teams(githubID)
	if err != nil {
		return nil, false, err
	}
	for _, t := range list {
		members, err := s.store.TeamMembers(t.OwnerID)
		if err != nil {
			return nil, false, err
		}
		if members == nil {
			members = []TeamMember{}
		}
		teams = append(teams, TeamInfo{Owner: t.OwnerID, Name: t.Name, Org: t.Org, Seats: s.store.TeamSeats(t.OwnerID), Members: members})
	}
	return teams, mayOwn, nil
}

// teamMemberOf reads who an admin adds: an account id as it is, else a GitHub login resolved to
// its id (with the maintainer token: 5000 calls an hour).
func (s *Server) teamMemberOf(ctx context.Context, who string) (TeamMember, error) {
	who = strings.TrimSpace(who)
	if isAccountID(who) {
		return TeamMember{Kind: vipAccount, ID: who}, nil
	}
	if who == "" {
		return TeamMember{}, i18n.New("a GitHub login or an account id is needed")
	}
	g := s.GitHub
	id, login, err := githubAccount(ctx, g.client, g.apiBase, "users", who, g.cfg.Token)
	if err != nil {
		return TeamMember{}, err
	}
	return TeamMember{Kind: vipGitHub, ID: id, Login: login}, nil
}

// memberAccounts of a member: the accounts its level moves on (its id, or the accounts linked to
// its GitHub id).
func (s *Server) memberAccounts(m TeamMember) []string {
	if m.Kind == vipAccount {
		return []string{m.ID}
	}
	accounts, _ := s.store.LinkedAccounts(m.ID)
	return accounts
}

// teamMessage answers the team messages of the panel: `team` lists the teams, `teamAdd` and
// `teamRemove` change one the account manages. Only a device that proved its key may.
func (s *Server) teamMessage(c *identityConn, m idMsg) idMsg {
	fail := func(err error) idMsg { return idMsg{Type: "error", Message: i18n.Translate(c.lang, err)} }
	if s.GitHub == nil {
		return fail(errors.New("this relay has no GitHub link"))
	}
	if c.account == "" || c.key == "" {
		// A link that proved no key sees no team (the panel asks on every GitHub answer).
		if m.Type == "team" {
			return idMsg{Type: "team", Account: c.account}
		}
		return fail(i18n.New("only a device of the account can manage its team"))
	}
	if m.Type != "team" {
		teams, _, err := s.adminTeams(c.account)
		if err != nil {
			return fail(err)
		}
		var team *TeamInfo
		for i := range teams {
			if teams[i].Owner == m.Owner {
				team = &teams[i]
			}
		}
		if team == nil {
			return fail(i18n.New("this account does not manage that team"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// A removal names the member as the list gave it (its kind and id); an addition is typed.
		member := TeamMember{Kind: m.Kind, ID: m.Member}
		if m.Type == "teamAdd" || member.Kind == "" {
			if member, err = s.teamMemberOf(ctx, m.Member); err != nil {
				return fail(err)
			}
		}
		if m.Type == "teamAdd" {
			err = s.store.AddTeamMember(team.Owner, member)
		} else {
			var ok bool
			ok, err = s.store.RemoveTeamMember(team.Owner, member.Kind, member.ID)
			if err == nil && !ok {
				err = i18n.New("not a member of this team")
			}
		}
		if err != nil {
			return fail(err)
		}
		s.tellQuota(s.memberAccounts(member))
	}
	teams, mayOwn, err := s.adminTeams(c.account)
	if err != nil {
		return fail(err)
	}
	return idMsg{Type: "team", Account: c.account, Teams: teams, MayOwn: mayOwn}
}
