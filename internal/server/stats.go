package server

import (
	"sort"
	"strings"
	"time"

	"barreplays/internal/index"
)

// The aggregates here answer "how do I actually do?" across a replay folder:
// an overall record, and a breakdown by map and by faction. They are computed
// on request rather than stored — the index is already in memory, and a few
// hundred records is nothing to walk.

// playerRecord is a win/loss tally.
type playerRecord struct {
	Played  int     `json:"played"`
	Won     int     `json:"won"`
	Lost    int     `json:"lost"`
	WinRate float64 `json:"winRate"`
}

func (r *playerRecord) add(won bool) {
	r.Played++
	if won {
		r.Won++
	} else {
		r.Lost++
	}
	r.WinRate = float64(r.Won) / float64(r.Played)
}

// groupRecord is a record attached to a name — a map or a faction.
type groupRecord struct {
	Name string `json:"name"`
	playerRecord
}

// statsResponse is the dashboard payload.
type statsResponse struct {
	PlayerName string `json:"playerName"`
	// Decided counts matches with a recorded result that the player took part
	// in; matches without an outcome cannot be scored either way.
	Decided  int           `json:"decided"`
	Skipped  int           `json:"skipped"`
	Overall  playerRecord  `json:"overall"`
	Maps     []groupRecord `json:"maps"`
	Factions []groupRecord `json:"factions"`
	// Recent is the player's most recent results, oldest last, as a run of
	// wins and losses.
	Recent []recentResult `json:"recent"`
}

type recentResult struct {
	ID     string `json:"id"`
	Map    string `json:"map"`
	Played string `json:"played"`
	Won    bool   `json:"won"`
}

// maxRecent bounds the form guide shown on the dashboard.
const maxRecent = 30

// buildStats aggregates a player's record over the indexed replays.
func buildStats(records []*index.Record, playerName string) statsResponse {
	out := statsResponse{PlayerName: playerName, Maps: []groupRecord{}, Factions: []groupRecord{}, Recent: []recentResult{}}
	if playerName == "" {
		return out
	}

	byMap := map[string]*playerRecord{}
	byFaction := map[string]*playerRecord{}

	for _, rec := range records {
		r := rec.Replay
		team := findPlayerTeam(rec, playerName)
		if team == nil {
			continue
		}
		// An undecided match — quit early, or a recording that never
		// finalised — is neither a win nor a loss.
		if !r.HasStats || len(r.WinningAllyTeams) == 0 {
			out.Skipped++
			continue
		}

		won := team.Won
		out.Decided++
		out.Overall.add(won)

		if r.Map != "" {
			if byMap[r.Map] == nil {
				byMap[r.Map] = &playerRecord{}
			}
			byMap[r.Map].add(won)
		}
		if team.Side != "" {
			if byFaction[team.Side] == nil {
				byFaction[team.Side] = &playerRecord{}
			}
			byFaction[team.Side].add(won)
		}
		if len(out.Recent) < maxRecent {
			out.Recent = append(out.Recent, recentResult{
				ID: r.ID, Map: r.Map, Played: r.Played.Format(time.RFC3339), Won: won,
			})
		}
	}

	out.Maps = sortedRecords(byMap)
	out.Factions = sortedRecords(byFaction)
	return out
}

// sortedRecords orders groups by how often they were played, then by name, so
// the ordering does not jitter between requests.
func sortedRecords(m map[string]*playerRecord) []groupRecord {
	out := make([]groupRecord, 0, len(m))
	for name, rec := range m {
		out = append(out, groupRecord{Name: name, playerRecord: *rec})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Played != out[j].Played {
			return out[i].Played > out[j].Played
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// findPlayerTeam returns the team the named player controlled, or nil if they
// were not in this match.
//
// Teams are named after their controlling player, so matching on the team name
// also excludes matches the player only spectated.
func findPlayerTeam(rec *index.Record, playerName string) *teamRef {
	for i := range rec.Replay.Teams {
		t := &rec.Replay.Teams[i]
		if !t.IsAI && strings.EqualFold(t.Name, playerName) {
			return &teamRef{Won: t.Won, Side: t.Side}
		}
	}
	return nil
}

// teamRef is the slice of a team the aggregates need.
type teamRef struct {
	Won  bool
	Side string
}
