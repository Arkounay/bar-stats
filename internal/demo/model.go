package demo

import "time"

// Replay is a fully decoded demo file.
//
// Summary-only decodes ([ParseSummary]) populate everything except Teams'
// sample series, WinningAllyTeams and player statistics; HasStats reports
// whether the statistics chunks were present at all.
type Replay struct {
	// Identity
	ID       string    `json:"id"`
	FileName string    `json:"fileName"`
	Path     string    `json:"path"`
	FileSize int64     `json:"fileSize"`
	GameID   string    `json:"gameID"`
	Played   time.Time `json:"played"`

	// Setup
	Engine string `json:"engine"`
	Game   string `json:"game"`
	Map    string `json:"map"`

	// Outcome. DurationSeconds is the recorded match length; it is zero for
	// demos whose recording never finalised.
	DurationSeconds  int   `json:"durationSeconds"`
	WinningAllyTeams []int `json:"winningAllyTeams"`

	// HasStats reports whether the trailing team-statistics chunk was written.
	// Aborted recordings produce a readable demo with no statistics at all.
	HasStats bool `json:"hasStats"`
	// SamplePeriod is the interval between samples, in seconds.
	SamplePeriod int `json:"samplePeriod"`

	AllyTeams []AllyTeam `json:"allyTeams"`
	Teams     []Team     `json:"teams"`
	Players   []Player   `json:"players"`

	// Spectators are players not assigned to any team.
	Spectators []Player `json:"spectators"`
}

// AllyTeam is a side of the match — the unit of winning and losing.
type AllyTeam struct {
	ID    int  `json:"id"`
	Won   bool `json:"won"`
	TeamIDs []int `json:"teamIDs"`
}

// Team is one commander slot. Each team owns one statistics series.
type Team struct {
	ID         int    `json:"id"`
	AllyTeamID int    `json:"allyTeamID"`
	// Name is the controlling player's or AI's name.
	Name  string `json:"name"`
	Side  string `json:"side"`
	Color string `json:"color"`
	IsAI  bool   `json:"isAI"`
	Won   bool   `json:"won"`

	// Final is the last statistics sample, i.e. the team's totals for the
	// match. It is retained when Samples are dropped, so a listing can show
	// end-of-match figures without holding every series in memory.
	Final Sample `json:"final"`

	// Samples is the statistics time series, one entry every
	// [Replay.SamplePeriod] seconds. Empty on summary decodes and on demos
	// with no statistics chunk.
	Samples []Sample `json:"-"`
}

// StripSamples drops the per-team time series, keeping the totals. Used to
// hold a whole demo folder in memory at a fraction of the cost; the series is
// re-read from disk when a replay is actually opened.
func (r *Replay) StripSamples() {
	for i := range r.Teams {
		r.Teams[i].Samples = nil
	}
}

// Player is a human participant. A player controlling a team is linked to it
// by TeamID; spectators have TeamID -1.
//
// Rank and Skill are written by the lobby that hosted the match, so they are
// absent from local skirmishes and from replays hosted by older lobbies. Rank
// is -1 and the skill fields empty when unavailable, rather than zero, which
// is a legitimate rating.
type Player struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	TeamID    int    `json:"teamID"`
	Spectator bool   `json:"spectator"`
	// Rank is the in-game chevron level, or -1 if unknown.
	Rank int `json:"rank"`
	// Skill is the lobby's OpenSkill rating, kept as written ("31.18").
	Skill string `json:"skill"`
	// SkillUncertainty qualifies Skill: a high value means a provisional
	// rating the lobby is still converging on.
	SkillUncertainty string `json:"skillUncertainty"`
	// Stats is the recorded input activity; zero on summary decodes.
	Stats PlayerStats `json:"stats"`
}

// Duration returns the match length.
func (r *Replay) Duration() time.Duration {
	return time.Duration(r.DurationSeconds) * time.Second
}

// TeamByID returns the team with the given engine team ID.
func (r *Replay) TeamByID(id int) *Team {
	for i := range r.Teams {
		if r.Teams[i].ID == id {
			return &r.Teams[i]
		}
	}
	return nil
}
