package server

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"barreplays/internal/demo"
	"barreplays/internal/index"
)

// The types here are the wire format between the Go backend and the browser
// UI. They are deliberately separate from the domain types in package demo:
// the UI's needs (flattened series arrays, precomputed labels) differ from the
// decoder's, and pinning them apart means a decoder change does not silently
// reshape the API.

// replayListItem is one row of the replay list.
type replayListItem struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Played   string `json:"played"`
	Map      string `json:"map"`
	Engine   string `json:"engine"`
	Duration int    `json:"durationSeconds"`
	// Enriched reports whether the background pass has read this replay's
	// outcome yet; the UI shows a placeholder until it has.
	Enriched    bool `json:"enriched"`
	HasStats    bool `json:"hasStats"`
	PlayerCount int  `json:"playerCount"`
	AICount     int  `json:"aiCount"`
	// Format is the match shape, e.g. "8v8" or "FFA · 6 players".
	Format      string        `json:"format"`
	AllyTeams  []allyTeamRow `json:"allyTeams"`
	FileSize   int64         `json:"fileSize"`

	// Outcome for the configured player. YouPlayed is false when they were
	// not in this match; YouWon is only meaningful when YouPlayed is true and
	// the match has a recorded result.
	YouPlayed bool `json:"youPlayed"`
	YouWon    bool `json:"youWon"`
	// Decided reports whether the match has a result to score at all.
	Decided bool `json:"decided"`
}

// allyTeamRow summarises a side for the list view.
type allyTeamRow struct {
	ID    int      `json:"id"`
	Won   bool     `json:"won"`
	Names []string `json:"names"`
}

// matchFormat describes the shape of a match — "8v8", "FFA", "4v4v4" — from
// how its teams are distributed across ally teams.
//
// Empty ally teams are ignored: a lobby can declare more sides than it fills,
// and counting those would turn an ordinary 8v8 into a three-way.
func matchFormat(r *demo.Replay, teams map[int]*demo.Team) string {
	var sizes []int
	allAI := 0
	for _, at := range r.AllyTeams {
		n, ai := 0, 0
		for _, tid := range at.TeamIDs {
			if t, ok := teams[tid]; ok {
				n++
				if t.IsAI {
					ai++
				}
			}
		}
		if n == 0 {
			continue
		}
		sizes = append(sizes, n)
		if ai == n {
			allAI++
		}
	}
	if len(sizes) == 0 {
		return ""
	}

	label := ""
	switch {
	case len(sizes) == 1:
		label = fmt.Sprintf("%d-player co-op", sizes[0])
	case len(sizes) == 2:
		label = fmt.Sprintf("%dv%d", sizes[0], sizes[1])
	case !slices.ContainsFunc(sizes, func(n int) bool { return n != 1 }):
		// Three or more sides of one player each is a free-for-all.
		label = fmt.Sprintf("FFA · %d players", len(sizes))
	case len(sizes) <= 4:
		parts := make([]string, len(sizes))
		for i, n := range sizes {
			parts[i] = strconv.Itoa(n)
		}
		label = strings.Join(parts, "v")
	default:
		label = fmt.Sprintf("%d-way team FFA", len(sizes))
	}

	// Flag the case where a whole side is AI, which changes what the numbers
	// mean without changing them.
	if allAI > 0 && allAI < len(sizes) {
		label += " vs AI"
	}
	return label
}

// teamIndex maps team IDs to teams for repeated lookup.
func teamIndex(r *demo.Replay) map[int]*demo.Team {
	out := make(map[int]*demo.Team, len(r.Teams))
	for i := range r.Teams {
		out[r.Teams[i].ID] = &r.Teams[i]
	}
	return out
}

// (free-for-all detection above uses slices.ContainsFunc directly)

func newReplayListItem(rec *index.Record, playerName string) replayListItem {
	r := rec.Replay
	item := replayListItem{
		ID:       r.ID,
		FileName: r.FileName,
		Played:   r.Played.Format(time.RFC3339),
		Map:      r.Map,
		Engine:   r.Engine,
		Duration: r.DurationSeconds,
		Enriched: rec.Enriched,
		HasStats: r.HasStats,
		FileSize: r.FileSize,
	}
	for _, t := range r.Teams {
		if t.IsAI {
			item.AICount++
		} else {
			item.PlayerCount++
		}
	}
	// TeamByID is a linear scan, so index once rather than scanning per member
	// — a 32-team match would otherwise cost a thousand comparisons per row.
	teams := teamIndex(r)
	for _, at := range r.AllyTeams {
		row := allyTeamRow{ID: at.ID, Won: at.Won}
		for _, tid := range at.TeamIDs {
			if t, ok := teams[tid]; ok {
				row.Names = append(row.Names, t.Name)
			}
		}
		item.AllyTeams = append(item.AllyTeams, row)
	}

	item.Format = matchFormat(r, teams)
	item.Decided = r.HasStats && len(r.WinningAllyTeams) > 0
	if playerName != "" {
		if team := findPlayerTeam(rec, playerName); team != nil {
			item.YouPlayed = true
			item.YouWon = item.Decided && team.Won
		}
	}
	return item
}

// replayDetail is the full payload for one replay, including every metric's
// series for every team.
//
// All metrics are sent at once rather than per request: the whole payload is a
// few hundred kilobytes over localhost, and it makes switching metric or view
// in the UI instant instead of a round trip.
type replayDetail struct {
	ID           string       `json:"id"`
	FileName     string       `json:"fileName"`
	Path         string       `json:"path"`
	Played       string       `json:"played"`
	Map          string       `json:"map"`
	Engine       string       `json:"engine"`
	Game         string       `json:"game"`
	Duration     int          `json:"durationSeconds"`
	SamplePeriod int          `json:"samplePeriod"`
	Format       string       `json:"format"`
	HasStats     bool         `json:"hasStats"`
	Winners      []int        `json:"winningAllyTeams"`
	AllyTeams    []detailAlly `json:"allyTeams"`
	Teams        []detailTeam `json:"teams"`
	Spectators   []string     `json:"spectators"`

	// MapSize is the map's extent in world units, which is what lets start
	// positions be drawn on a picture of the map. Absent when it could not be
	// worked out, in which case the map is shown unannotated.
	MapSize *demo.MapSize `json:"mapSize,omitempty"`
}

type detailAlly struct {
	ID      int   `json:"id"`
	Won     bool  `json:"won"`
	TeamIDs []int `json:"teamIDs"`
}

// detailTeam carries one team's identity plus its full statistics series.
type detailTeam struct {
	ID         int     `json:"id"`
	AllyTeamID int     `json:"allyTeamID"`
	Name       string  `json:"name"`
	Side       string  `json:"side"`
	// Color is the team's in-game colour. The charts do not encode series by
	// it — in-game colours are arbitrary and fail contrast and
	// colour-blindness checks — but the roster shows it so a team can be
	// matched to what the player saw in the match.
	Color string `json:"color"`
	IsAI  bool   `json:"isAI"`
	Won   bool   `json:"won"`
	// IsYou marks the configured player's own team, so the UI can single it
	// out without the frontend needing to know the name-matching rule.
	IsYou bool    `json:"isYou"`
	APM   float64 `json:"apm"`
	// Lobby rating of the controlling player. Rank is -1 and the skill fields
	// empty when the hosting lobby did not record them.
	Rank             int    `json:"rank"`
	Skill            string `json:"skill"`
	SkillUncertainty string `json:"skillUncertainty"`

	// StartPos is where this team placed its commander, and FirstFactory the
	// first factory it ordered. Both come from the demo's packet stream rather
	// than its statistics, so both are absent for a match that ended before
	// they happened.
	StartPos     *demo.Position `json:"startPos,omitempty"`
	FirstFactory *openingBuild  `json:"firstFactory,omitempty"`

	// Times is this team's sample times in seconds; Series maps a metric key
	// to values at those times.
	Times  []float64            `json:"times"`
	Series map[string][]float64 `json:"series"`
	// Totals is the end-of-match value per metric.
	Totals map[string]float64 `json:"totals"`
}

// openingBuild is a build order flattened for the UI. The decoder keeps a
// frame counter; the browser wants seconds and the pre-game distinction
// already made, so it does not need to know the simulation rate.
type openingBuild struct {
	Unit string `json:"unit"`
	// Kind is the sort of factory, in the words players use for them — the
	// label the map draws under each start.
	Kind    string  `json:"kind"`
	Seconds float64 `json:"seconds"`
	// PreGame marks an order queued during start position placement rather
	// than made under the clock.
	PreGame bool `json:"preGame"`
}

func newOpeningBuild(b *demo.BuildOrder) *openingBuild {
	if b == nil {
		return nil
	}
	return &openingBuild{Unit: b.Unit, Kind: b.Kind, Seconds: b.Seconds(), PreGame: b.PreGame()}
}

func newReplayDetail(r *demo.Replay, playerName string) replayDetail {
	d := replayDetail{
		ID:           r.ID,
		FileName:     r.FileName,
		Path:         r.Path,
		Played:       r.Played.Format(time.RFC3339),
		Map:          r.Map,
		Engine:       r.Engine,
		Game:         r.Game,
		Duration:     r.DurationSeconds,
		SamplePeriod: r.SamplePeriod,
		Format:       matchFormat(r, teamIndex(r)),
		HasStats:     r.HasStats,
		Winners:      r.WinningAllyTeams,
		MapSize:      r.MapSize,
	}
	for _, at := range r.AllyTeams {
		d.AllyTeams = append(d.AllyTeams, detailAlly{ID: at.ID, Won: at.Won, TeamIDs: at.TeamIDs})
	}
	for _, s := range r.Spectators {
		d.Spectators = append(d.Spectators, s.Name)
	}

	// Index players by team so their activity and rating can be shown beside
	// the team's production figures.
	byTeam := map[int]demo.Player{}
	for _, p := range r.Players {
		byTeam[p.TeamID] = p
	}

	for _, t := range r.Teams {
		dt := detailTeam{
			ID: t.ID, AllyTeamID: t.AllyTeamID, Name: t.Name, Side: t.Side,
			Color: t.Color, IsAI: t.IsAI, Won: t.Won,
			IsYou:  playerName != "" && !t.IsAI && strings.EqualFold(t.Name, playerName),
			Rank:   -1,
			Series: map[string][]float64{},
			Totals: map[string]float64{},

			StartPos:     t.StartPos,
			FirstFactory: newOpeningBuild(t.FirstFactory),
		}
		if p, ok := byTeam[t.ID]; ok {
			dt.APM = p.Stats.APM(float64(r.DurationSeconds))
			dt.Rank = p.Rank
			dt.Skill = p.Skill
			dt.SkillUncertainty = p.SkillUncertainty
		}

		dt.Times = make([]float64, len(t.Samples))
		for i, s := range t.Samples {
			dt.Times[i] = s.Seconds()
		}
		for _, m := range demo.Metrics {
			values := make([]float64, len(t.Samples))
			for i, s := range t.Samples {
				values[i] = m.Value(s)
			}
			dt.Series[m.Key] = values
			dt.Totals[m.Key] = m.Value(t.Final)
		}
		d.Teams = append(d.Teams, dt)
	}
	return d
}

// metricCatalogue publishes the metric registry so the UI builds its charts,
// roster columns and headline tiles from the backend rather than from its own
// copy of the list.
//
// demo.Metric is serialised directly: it already carries JSON tags, including
// `json:"-"` on the extractor. A hand-copied wire struct here would mean every
// new registry field compiled, passed tests, and silently never reached the
// browser.
func metricCatalogue() ([]demo.Metric, []string) {
	groups := make([]string, 0, len(demo.MetricGroups))
	for _, g := range demo.MetricGroups {
		groups = append(groups, string(g))
	}
	return demo.Metrics, groups
}
