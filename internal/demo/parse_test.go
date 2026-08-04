package demo

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// buildDemo assembles a syntactically valid demo file in memory.
//
// The point of the test is the byte layout: the decoder's correctness rests
// entirely on field offsets matching the engine's structs, and a synthetic
// file pins those offsets down without needing a real match on disk.
func buildDemo(t *testing.T, script string, teamSamples [][]Sample, winners []byte, players []PlayerStats) []byte {
	t.Helper()

	var stream []byte // stand-in for the network packet stream
	stream = append(stream, bytes.Repeat([]byte{0xAB}, 64)...)

	var teamStats bytes.Buffer
	for _, samples := range teamSamples {
		if err := binary.Write(&teamStats, byteOrder, int32(len(samples))); err != nil {
			t.Fatal(err)
		}
	}
	for _, samples := range teamSamples {
		for _, s := range samples {
			writeSample(t, &teamStats, s)
		}
	}

	var playerStats bytes.Buffer
	for _, p := range players {
		for _, v := range []int32{p.MousePixels, p.MouseClicks, p.KeyPresses, p.NumCommands, p.UnitCommands} {
			if err := binary.Write(&playerStats, byteOrder, v); err != nil {
				t.Fatal(err)
			}
		}
	}

	h := fileHeader{
		Version:              SupportedVersion,
		HeaderSize:           headerSize,
		UnixTime:             1700000000,
		ScriptSize:           int32(len(script)),
		DemoStreamSize:       int32(len(stream)),
		GameTime:             600,
		WallclockTime:        610,
		NumPlayers:           int32(len(players)),
		PlayerStatSize:       int32(playerStats.Len()),
		PlayerStatElemSize:   20,
		NumTeams:             int32(len(teamSamples)),
		TeamStatSize:         int32(teamStats.Len()),
		TeamStatElemSize:     sampleSize,
		TeamStatPeriod:       15,
		WinningAllyTeamsSize: int32(len(winners)),
	}
	copy(h.Magic[:], Magic)
	copy(h.EngineVersion[:], "test-engine")

	var out bytes.Buffer
	if err := binary.Write(&out, byteOrder, &h); err != nil {
		t.Fatal(err)
	}
	if out.Len() != headerSize {
		t.Fatalf("encoded header is %d bytes, expected %d", out.Len(), headerSize)
	}
	out.WriteString(script)
	out.Write(stream)
	out.Write(winners)
	out.Write(playerStats.Bytes())
	out.Write(teamStats.Bytes())
	return out.Bytes()
}

func writeSample(t *testing.T, w *bytes.Buffer, s Sample) {
	t.Helper()
	fields := []any{
		s.Frame,
		s.MetalUsed, s.EnergyUsed, s.MetalProduced, s.EnergyProduced,
		s.MetalExcess, s.EnergyExcess, s.MetalReceived, s.EnergyReceived,
		s.MetalSent, s.EnergySent, s.DamageDealt, s.DamageReceived,
		s.UnitsProduced, s.UnitsDied, s.UnitsReceived, s.UnitsSent,
		s.UnitsCaptured, s.UnitsOutCaptured, s.UnitsKilled,
	}
	before := w.Len()
	for _, f := range fields {
		if err := binary.Write(w, byteOrder, f); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.Len() - before; got != sampleSize {
		t.Fatalf("encoded sample is %d bytes, expected %d", got, sampleSize)
	}
}

const testScript = `[GAME]
{
	mapname=Test Map 1.0;
	gametype=Test Game;
	[ALLYTEAM0] { numallies=0; }
	[ALLYTEAM1] { numallies=0; }
	[TEAM0]
	{
		allyteam=0;
		rgbcolor=1.00000 0.50000 0.00000;
		side=Armada;
	}
	[TEAM1]
	{
		allyteam=1;
		rgbcolor=0.00000 0.00000 1.00000;
		side=Cortex;
	}
	[PLAYER0]
	{
		name=Alice;
		team=0;
		spectator=0;
	}
	[PLAYER1]
	{
		name=Watcher;
		spectator=1;
	}
	[AI0]
	{
		name=EnemyBot;
		team=1;
		shortname=BARb;
	}
}
`

func writeTemp(t *testing.T, name string, data []byte, compress bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if compress {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		data = buf.Bytes()
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseFullDemo(t *testing.T) {
	samples := [][]Sample{
		{
			{Frame: 0},
			{Frame: 450, MetalProduced: 100, EnergyProduced: 2000, DamageDealt: 50, UnitsProduced: 3},
			{Frame: 900, MetalProduced: 250, EnergyProduced: 5000, DamageDealt: 175, UnitsProduced: 8, UnitsKilled: 2},
		},
		{
			{Frame: 0},
			{Frame: 450, MetalProduced: 80, EnergyProduced: 1500},
			{Frame: 900, MetalProduced: 190, EnergyProduced: 3900, UnitsDied: 4},
		},
	}
	players := []PlayerStats{
		{MousePixels: 1000, MouseClicks: 200, KeyPresses: 50, NumCommands: 600, UnitCommands: 400},
		{MousePixels: 10, MouseClicks: 1, KeyPresses: 0, NumCommands: 0, UnitCommands: 0},
	}
	raw := buildDemo(t, testScript, samples, []byte{0}, players)

	for _, tc := range []struct {
		name     string
		compress bool
	}{
		{"compressed", true},
		{"uncompressed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "match"+Ext, raw, tc.compress)
			r, err := Parse(path)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			if r.Map != "Test Map 1.0" {
				t.Errorf("Map = %q, want %q", r.Map, "Test Map 1.0")
			}
			if r.DurationSeconds != 600 {
				t.Errorf("DurationSeconds = %d, want 600", r.DurationSeconds)
			}
			if !r.HasStats {
				t.Error("HasStats = false, want true")
			}
			if r.SamplePeriod != 15 {
				t.Errorf("SamplePeriod = %d, want 15", r.SamplePeriod)
			}

			if len(r.Teams) != 2 {
				t.Fatalf("got %d teams, want 2", len(r.Teams))
			}
			if r.Teams[0].Name != "Alice" {
				t.Errorf("team 0 name = %q, want Alice", r.Teams[0].Name)
			}
			if r.Teams[0].Color != "#ff8000" {
				t.Errorf("team 0 colour = %q, want #ff8000", r.Teams[0].Color)
			}
			if r.Teams[0].IsAI {
				t.Error("team 0 should be human")
			}
			if !r.Teams[1].IsAI || r.Teams[1].Name != "EnemyBot" {
				t.Errorf("team 1 = %q (AI %v), want EnemyBot AI", r.Teams[1].Name, r.Teams[1].IsAI)
			}

			// Winner propagates from the ally team to its member teams.
			if !r.Teams[0].Won || r.Teams[1].Won {
				t.Errorf("winner flags wrong: team0=%v team1=%v", r.Teams[0].Won, r.Teams[1].Won)
			}
			if len(r.WinningAllyTeams) != 1 || r.WinningAllyTeams[0] != 0 {
				t.Errorf("WinningAllyTeams = %v, want [0]", r.WinningAllyTeams)
			}

			// Field offsets: the whole decode hinges on these landing right.
			got := r.Teams[0].Samples
			if len(got) != 3 {
				t.Fatalf("got %d samples, want 3", len(got))
			}
			if got[2].MetalProduced != 250 || got[2].EnergyProduced != 5000 {
				t.Errorf("sample 2 economy = %v/%v, want 250/5000", got[2].MetalProduced, got[2].EnergyProduced)
			}
			if got[2].UnitsProduced != 8 || got[2].UnitsKilled != 2 {
				t.Errorf("sample 2 units = %d/%d, want 8/2", got[2].UnitsProduced, got[2].UnitsKilled)
			}
			if got[1].Seconds() != 15 {
				t.Errorf("sample 1 time = %v, want 15", got[1].Seconds())
			}
			if r.Teams[0].Final.MetalProduced != 250 {
				t.Errorf("Final.MetalProduced = %v, want 250", r.Teams[0].Final.MetalProduced)
			}

			// Spectators are kept apart from players.
			if len(r.Players) != 1 || r.Players[0].Name != "Alice" {
				t.Errorf("players = %+v, want just Alice", r.Players)
			}
			if len(r.Spectators) != 1 || r.Spectators[0].Name != "Watcher" {
				t.Errorf("spectators = %+v, want just Watcher", r.Spectators)
			}
			if r.Players[0].Stats.NumCommands != 600 {
				t.Errorf("Alice commands = %d, want 600", r.Players[0].Stats.NumCommands)
			}
			if apm := r.Players[0].Stats.APM(600); apm != 60 {
				t.Errorf("APM = %v, want 60", apm)
			}
		})
	}
}

// TestParseSummarySkipsStats covers the fast listing path, which must stop
// before the demo stream and therefore reports no statistics.
func TestParseSummarySkipsStats(t *testing.T) {
	raw := buildDemo(t, testScript, [][]Sample{{{Frame: 0}}, {{Frame: 0}}}, []byte{0}, nil)
	path := writeTemp(t, "match"+Ext, raw, true)

	r, err := ParseSummary(path)
	if err != nil {
		t.Fatalf("ParseSummary: %v", err)
	}
	if r.Map != "Test Map 1.0" {
		t.Errorf("Map = %q", r.Map)
	}
	if len(r.Teams[0].Samples) != 0 {
		t.Error("summary parse should not read sample series")
	}
	if len(r.WinningAllyTeams) != 0 {
		t.Error("summary parse should not read the winner list")
	}
}

// TestParseAbortedRecording covers a match that was quit out of: the header is
// valid but every trailing statistics chunk is empty.
func TestParseAbortedRecording(t *testing.T) {
	raw := buildDemo(t, testScript, nil, nil, nil)
	path := writeTemp(t, "aborted"+Ext, raw, true)

	r, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.HasStats {
		t.Error("HasStats = true, want false for an aborted recording")
	}
	if len(r.Teams) != 2 {
		t.Errorf("setup should still decode: got %d teams", len(r.Teams))
	}
}

func TestParseRejectsForeignFiles(t *testing.T) {
	path := writeTemp(t, "junk"+Ext, bytes.Repeat([]byte("not a demo!"), 64), true)
	if _, err := Parse(path); err == nil {
		t.Fatal("expected an error for a non-demo file")
	}
}

func TestParseRejectsUnsupportedVersion(t *testing.T) {
	raw := buildDemo(t, testScript, nil, nil, nil)
	byteOrder.PutUint32(raw[16:], uint32(SupportedVersion+1))
	path := writeTemp(t, "future"+Ext, raw, true)

	_, err := Parse(path)
	if err == nil {
		t.Fatal("expected an error for an unsupported version")
	}
	var verr *UnsupportedVersionError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %v, want UnsupportedVersionError", err)
	}
}

// TestMetricsCoverSample guards the registry against a field being added to
// Sample without becoming visible in the UI.
func TestMetricsCoverSample(t *testing.T) {
	s := Sample{MetalProduced: 12, DamageDealt: 34, UnitsKilled: 56}
	for _, key := range []string{"metalProduced", "damageDealt", "unitsKilled"} {
		m, ok := MetricByKey(key)
		if !ok {
			t.Fatalf("metric %q not registered", key)
		}
		if m.Value(s) == 0 {
			t.Errorf("metric %q extracted 0 from a populated sample", key)
		}
	}
	seen := map[string]bool{}
	for _, m := range Metrics {
		if seen[m.Key] {
			t.Errorf("duplicate metric key %q", m.Key)
		}
		seen[m.Key] = true
		if m.Value == nil {
			t.Errorf("metric %q has no extractor", m.Key)
		}
		if math.IsNaN(m.Value(s)) {
			t.Errorf("metric %q extracted NaN", m.Key)
		}
	}
}
