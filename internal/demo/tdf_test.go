package demo

import "testing"

func TestParseTDFNesting(t *testing.T) {
	src := `[GAME]
{
	mapname=Some Map;
	MyPlayerName=Host;
	[TEAM0]
	{
		allyteam=1;
		side=Cortex;
	}
	[TEAM1]
	{
		allyteam=0;
	}
}
`
	game := parseTDF(src).child("game")
	if game == nil {
		t.Fatal("no [GAME] section")
	}
	if got := game.str("mapname"); got != "Some Map" {
		t.Errorf("mapname = %q", got)
	}
	// Keys are matched case-insensitively; lobbies vary in how they spell them.
	if got := game.str("myplayername"); got != "Host" {
		t.Errorf("myplayername = %q", got)
	}
	teams := game.numberedChildren("team")
	if len(teams) != 2 {
		t.Fatalf("got %d teams, want 2", len(teams))
	}
	if got := teams[0].intOr("allyteam", -1); got != 1 {
		t.Errorf("team0 allyteam = %d, want 1", got)
	}
	if got := teams[1].intOr("missing", 7); got != 7 {
		t.Errorf("fallback not applied, got %d", got)
	}
}

// numberedChildren stops at the first gap, matching the engine's contiguous
// numbering; a stray high index must not be picked up.
func TestNumberedChildrenStopsAtGap(t *testing.T) {
	game := parseTDF(`[GAME]
{
	[TEAM0] { a=1; }
	[TEAM2] { a=1; }
}`).child("game")
	if got := len(game.numberedChildren("team")); got != 1 {
		t.Errorf("got %d teams, want 1", got)
	}
}

func TestParseTDFTolerated(t *testing.T) {
	// Malformed lines must not cost the surrounding values.
	game := parseTDF(`[GAME]
{
	mapname=Fine;
	this line has no equals sign
	// a comment
	gametype=Also Fine;
}`).child("game")
	if game.str("mapname") != "Fine" || game.str("gametype") != "Also Fine" {
		t.Errorf("valid keys lost: %q / %q", game.str("mapname"), game.str("gametype"))
	}
}

func TestBoolOr(t *testing.T) {
	s := parseTDF(`[A]
{
	yes=1;
	no=0;
	word=true;
}`).child("a")
	if !s.boolOr("yes", false) {
		t.Error("yes should be true")
	}
	if s.boolOr("no", true) {
		t.Error("no should be false")
	}
	if !s.boolOr("word", false) {
		t.Error("word should be true")
	}
	if !s.boolOr("absent", true) {
		t.Error("absent should fall back")
	}
}

func TestRGBHex(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.00000 1.00000 1.00000", "#ffffff"},
		{"0.00000 0.00000 0.00000", "#000000"},
		{"0.65098 0.69020 0.40402", "#a6b067"},
		{"1.0 0.5 0.0", "#ff8000"},
		{"", ""},
		{"0.5 0.5", ""},
		{"a b c", ""},
	}
	for _, c := range cases {
		if got := rgbHex(c.in); got != c.want {
			t.Errorf("rgbHex(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildAllyTeamsGroupsAndOrders(t *testing.T) {
	teams := []Team{
		{ID: 0, AllyTeamID: 1},
		{ID: 1, AllyTeamID: 0},
		{ID: 2, AllyTeamID: 1},
	}
	got := buildAllyTeams(teams, 2)
	if len(got) != 2 {
		t.Fatalf("got %d ally teams, want 2", len(got))
	}
	if got[0].ID != 0 || len(got[0].TeamIDs) != 1 || got[0].TeamIDs[0] != 1 {
		t.Errorf("ally 0 = %+v", got[0])
	}
	if got[1].ID != 1 || len(got[1].TeamIDs) != 2 {
		t.Errorf("ally 1 = %+v", got[1])
	}
}
