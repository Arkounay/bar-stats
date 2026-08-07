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

	// MapSpawns are the start points the map itself defines, in no particular
	// order. They are working for [estimateMapSize] and nothing reads them
	// afterwards, so they are not carried into the index cache. Empty when the
	// hosting lobby did not record them.
	MapSpawns []Position `json:"-"`

	// MapSize is the map's extent in world units, recovered from the symmetry
	// of MapSpawns. Nil when it could not be established — see
	// [estimateMapSize] — in which case a world coordinate cannot be placed on
	// a picture of the map.
	MapSize *MapSize `json:"mapSize,omitempty"`

	AllyTeams []AllyTeam `json:"allyTeams"`
	Teams     []Team     `json:"teams"`
	Players   []Player   `json:"players"`

	// Spectators are players not assigned to any team.
	Spectators []Player `json:"spectators"`
}

// AllyTeam is a side of the match — the unit of winning and losing.
type AllyTeam struct {
	ID      int   `json:"id"`
	Won     bool  `json:"won"`
	TeamIDs []int `json:"teamIDs"`
}

// Position is a point on the map, in the engine's world units. X and Z are the
// engine's two horizontal axes; the vertical one is terrain height, which
// nothing here has a use for.
type Position struct {
	X float32 `json:"x"`
	Z float32 `json:"z"`
}

// MapSize is a map's extent in world units — the span a start position's
// coordinates are measured against.
//
// It is worked out from the replay rather than read from it; see
// [estimateMapSize].
type MapSize struct {
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
	// Approximate reports that the figures are a measurement rather than the
	// map's real dimensions, so anything drawn against them sits close to the
	// right place rather than exactly on it.
	Approximate bool `json:"approximate"`
}

// BuildOrder is one build command recovered from the demo stream.
//
// It records an intention, not an outcome: the order was issued, but the
// building may have been cancelled, blocked by terrain, or destroyed
// part-built. Nothing in the demo stream reports whether it finished.
type BuildOrder struct {
	// Unit is the engine's internal unit name, for example "armlab".
	Unit string `json:"unit"`
	// Kind is what sort of factory that is, in the words players use for them
	// — "bot", "vehicle", "air", "sea", "hover". Empty for a unit whose name
	// does not resolve; see [factoryKind].
	Kind string `json:"kind"`
	// Frame is the simulation frame the order reached the game on. Orders
	// queued during the pre-game placement phase are delivered in the first
	// frames after the match starts, so they land just above zero rather than
	// at it — see [BuildOrder.PreGame].
	Frame int32 `json:"frame"`
}

// Seconds is the game time the order was issued at, which is negative for a
// pre-game order.
func (b BuildOrder) Seconds() float64 { return float64(b.Frame) / simFPS }

// preGameFrames is how much of the simulation counts as "the order was already
// queued". The engine flushes orders made during start position placement over
// the first frames of the match, so they arrive at frame 5 or 10 rather than
// at 0; a second is comfortably longer than that flush and far shorter than
// the time a player needs to see the map and place a factory.
const preGameFrames = simFPS

// PreGame reports an order queued during start position placement rather than
// played out in the match. Around half of all opening factories are pre-queued
// this way, so the distinction is worth drawing: it separates a planned
// opening from one made under the clock.
func (b BuildOrder) PreGame() bool { return b.Frame < preGameFrames }

// Team is one commander slot. Each team owns one statistics series.
type Team struct {
	ID         int `json:"id"`
	AllyTeamID int `json:"allyTeamID"`
	// Name is the controlling player's or AI's name.
	Name  string `json:"name"`
	Side  string `json:"side"`
	Color string `json:"color"`
	IsAI  bool   `json:"isAI"`
	Won   bool   `json:"won"`

	// StartPos is where the team placed its commander, as broadcast during the
	// pre-game placement phase. Nil for a match that never got that far, and
	// on summary decodes.
	StartPos *Position `json:"startPos,omitempty"`
	// FirstFactory is the first factory this team ordered. Nil when the team
	// ordered none — a very short match, or a slot nobody played — and on
	// summary decodes.
	FirstFactory *BuildOrder `json:"firstFactory,omitempty"`

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
