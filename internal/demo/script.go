package demo

import (
	"slices"
	"strconv"
	"strings"
)

// applyScript decodes the embedded start script into the replay's setup —
// map, teams, players and ally teams.
//
// The script is authored by whichever lobby hosted the match, so fields are
// treated as optional throughout: a missing colour or side degrades that one
// value rather than failing the decode.
func applyScript(r *Replay, script string) {
	game := parseTDF(script).child("game")
	if game == nil {
		return
	}

	r.Map = game.str("mapname")
	r.Game = game.str("gametype")
	applyMapSpawns(r, game)

	// Teams must be built before players so a player can be attached to one.
	teamSections := game.numberedChildren("team")
	r.Teams = make([]Team, 0, len(teamSections))
	for i, ts := range teamSections {
		r.Teams = append(r.Teams, Team{
			ID:         i,
			AllyTeamID: ts.intOr("allyteam", 0),
			Side:       ts.str("side"),
			Color:      rgbHex(ts.str("rgbcolor")),
		})
	}

	// Human players. Spectators carry no team assignment.
	for i, ps := range game.numberedChildren("player") {
		p := Player{
			ID:        i,
			Name:      ps.str("name"),
			Spectator: ps.boolOr("spectator", false),
			TeamID:    ps.intOr("team", -1),
			Rank:      ps.intOr("rank", -1),
			// The lobby writes skill wrapped in brackets, e.g. "[28.5]".
			Skill:            strings.Trim(strings.TrimSpace(ps.str("skill")), "[]"),
			SkillUncertainty: strings.TrimSpace(ps.str("skilluncertainty")),
		}
		if p.Spectator {
			p.TeamID = -1
			r.Spectators = append(r.Spectators, p)
			continue
		}
		if t := r.TeamByID(p.TeamID); t != nil && t.Name == "" {
			t.Name = p.Name
		}
		r.Players = append(r.Players, p)
	}

	// AI players occupy a team slot without a corresponding human entry.
	for _, as := range game.numberedChildren("ai") {
		teamID := as.intOr("team", -1)
		if t := r.TeamByID(teamID); t != nil {
			t.IsAI = true
			if t.Name == "" {
				t.Name = as.str("name")
			}
			if t.Name == "" {
				t.Name = as.str("shortname")
			}
		}
	}

	// Any team still unnamed had no player or AI section; label it by index so
	// the UI never shows a blank series.
	for i := range r.Teams {
		if r.Teams[i].Name == "" {
			r.Teams[i].Name = "Team " + strconv.Itoa(r.Teams[i].ID)
		}
	}

	r.AllyTeams = buildAllyTeams(r.Teams, len(game.numberedChildren("allyteam")))
}

// buildAllyTeams groups teams by ally team. declared is the count of
// [ALLYTEAM*] sections, used so an ally team that lost every slot still
// appears.
func buildAllyTeams(teams []Team, declared int) []AllyTeam {
	byID := map[int]*AllyTeam{}
	var order []int
	ensure := func(id int) *AllyTeam {
		at, ok := byID[id]
		if !ok {
			at = &AllyTeam{ID: id}
			byID[id] = at
			order = append(order, id)
		}
		return at
	}
	for i := 0; i < declared; i++ {
		ensure(i)
	}
	for _, t := range teams {
		at := ensure(t.AllyTeamID)
		at.TeamIDs = append(at.TeamIDs, t.ID)
	}

	slices.Sort(order)
	out := make([]AllyTeam, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}
