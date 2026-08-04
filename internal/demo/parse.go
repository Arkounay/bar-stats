package demo

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Ext is the compressed demo extension BAR writes.
const Ext = ".sdfz"

// ParseSummary decodes only the metadata needed to list a replay: the header
// and the start script.
//
// It stops before the demo stream, so it reads a few hundred kilobytes rather
// than the whole file — the difference between an instant listing and a
// minute-long scan across a large demo folder. The returned replay has no
// statistics series and no winner; use [Parse] for those.
func ParseSummary(path string) (*Replay, error) {
	return parseFile(path, false)
}

// Parse fully decodes a replay, including the per-team statistics series.
//
// The demo stream sits between the script and the statistics and cannot be
// seeked past in a gzip stream, so this necessarily decompresses the entire
// file.
func Parse(path string) (*Replay, error) {
	return parseFile(path, true)
}

func parseFile(path string, full bool) (*Replay, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	r, err := decompress(bufio.NewReaderSize(f, 1<<16))
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}

	replay, err := parseStream(r, full)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	replay.Path = abs
	replay.FileName = filepath.Base(path)
	replay.FileSize = info.Size()
	replay.ID = idForPath(abs)
	return replay, nil
}

// idForPath derives a stable, URL-safe identifier from the demo's location.
// Demo file names contain spaces and punctuation, so they are unusable as
// path segments directly.
func idForPath(abs string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(abs)))
	return hex.EncodeToString(sum[:8])
}

// decompress transparently handles both the compressed (.sdfz) and raw (.sdf)
// forms by sniffing the gzip magic, rather than trusting the extension.
func decompress(br *bufio.Reader) (io.Reader, error) {
	magic, err := br.Peek(2)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if magic[0] == 0x1f && magic[1] == 0x8b {
		return gzip.NewReader(br)
	}
	return br, nil
}

func parseStream(r io.Reader, full bool) (*Replay, error) {
	var h fileHeader
	if err := binary.Read(r, byteOrder, &h); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if err := h.validate(); err != nil {
		return nil, err
	}

	replay := &Replay{
		GameID:          hex.EncodeToString(h.GameID[:]),
		Engine:          trimZero(h.EngineVersion[:]),
		Played:          time.Unix(h.UnixTime, 0),
		DurationSeconds: int(h.GameTime),
		SamplePeriod:    int(h.TeamStatPeriod),
		HasStats:        h.TeamStatSize > 0 && h.NumTeams > 0,
		WinningAllyTeams: []int{},
	}

	script := make([]byte, h.ScriptSize)
	if _, err := io.ReadFull(r, script); err != nil {
		return nil, fmt.Errorf("read start script: %w", err)
	}
	applyScript(replay, string(script))

	if !full {
		return replay, nil
	}

	// The demo stream is the bulk of the file and holds the raw network
	// packets. Every statistic we surface is in the trailing chunks, so it is
	// decompressed and thrown away rather than parsed.
	if _, err := io.CopyN(io.Discard, r, int64(h.DemoStreamSize)); err != nil {
		return nil, fmt.Errorf("skip demo stream: %w", err)
	}

	if err := readWinners(r, &h, replay); err != nil {
		return nil, err
	}
	if err := readPlayerStats(r, &h, replay); err != nil {
		return nil, err
	}
	if err := readTeamStats(r, &h, replay); err != nil {
		return nil, err
	}
	return replay, nil
}

// readWinners decodes the winning ally team list and marks the winning teams.
// The chunk is one byte per winning ally team, and is empty for a match that
// ended without a decided result.
func readWinners(r io.Reader, h *fileHeader, replay *Replay) error {
	if h.WinningAllyTeamsSize <= 0 {
		return nil
	}
	buf := make([]byte, h.WinningAllyTeamsSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("read winning ally teams: %w", err)
	}
	won := map[int]bool{}
	for _, b := range buf {
		id := int(b)
		won[id] = true
		replay.WinningAllyTeams = append(replay.WinningAllyTeams, id)
	}
	for i := range replay.AllyTeams {
		replay.AllyTeams[i].Won = won[replay.AllyTeams[i].ID]
	}
	for i := range replay.Teams {
		replay.Teams[i].Won = won[replay.Teams[i].AllyTeamID]
	}
	return nil
}

// readPlayerStats decodes the per-player input activity totals. The chunk is
// indexed by player ID, matching the [PLAYER*] sections of the start script.
func readPlayerStats(r io.Reader, h *fileHeader, replay *Replay) error {
	if h.PlayerStatSize <= 0 {
		return nil
	}
	buf := make([]byte, h.PlayerStatSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("read player stats: %w", err)
	}
	elem := int(h.PlayerStatElemSize)
	// Guard against a future engine growing the struct: decode the fields we
	// know and stride by the declared element size.
	if elem < 20 {
		return nil
	}
	for i := 0; i < int(h.NumPlayers); i++ {
		off := i * elem
		if off+20 > len(buf) {
			break
		}
		stats := PlayerStats{
			MousePixels:  int32(byteOrder.Uint32(buf[off:])),
			MouseClicks:  int32(byteOrder.Uint32(buf[off+4:])),
			KeyPresses:   int32(byteOrder.Uint32(buf[off+8:])),
			NumCommands:  int32(byteOrder.Uint32(buf[off+12:])),
			UnitCommands: int32(byteOrder.Uint32(buf[off+16:])),
		}
		assignPlayerStats(replay, i, stats)
	}
	return nil
}

func assignPlayerStats(replay *Replay, id int, stats PlayerStats) {
	for i := range replay.Players {
		if replay.Players[i].ID == id {
			replay.Players[i].Stats = stats
			return
		}
	}
	for i := range replay.Spectators {
		if replay.Spectators[i].ID == id {
			replay.Spectators[i].Stats = stats
			return
		}
	}
}

// readTeamStats decodes the statistics time series. The chunk is laid out as
// an int32 sample count per team, followed by each team's samples in order.
func readTeamStats(r io.Reader, h *fileHeader, replay *Replay) error {
	if h.TeamStatSize <= 0 || h.NumTeams <= 0 {
		return nil
	}
	buf := make([]byte, h.TeamStatSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("read team stats: %w", err)
	}

	numTeams := int(h.NumTeams)
	if len(buf) < numTeams*4 {
		return fmt.Errorf("team stats chunk too short for %d teams", numTeams)
	}
	counts := make([]int, numTeams)
	for i := range counts {
		counts[i] = int(int32(byteOrder.Uint32(buf[i*4:])))
	}

	off := numTeams * 4
	for teamID, count := range counts {
		if count < 0 || off+count*sampleSize > len(buf) {
			return fmt.Errorf("team %d declares %d samples, exceeding chunk", teamID, count)
		}
		samples := make([]Sample, count)
		for i := range samples {
			samples[i] = decodeSample(buf[off+i*sampleSize:])
		}
		off += count * sampleSize
		if t := replay.TeamByID(teamID); t != nil {
			t.Samples = samples
			if count > 0 {
				t.Final = samples[count-1]
			}
		}
	}

	// A finalised recording with no GameTime still has samples; derive the
	// duration from the last frame so the UI shows a length.
	if replay.DurationSeconds == 0 {
		replay.DurationSeconds = int(lastFrameSeconds(replay))
	}
	return nil
}

func lastFrameSeconds(replay *Replay) float64 {
	var max float64
	for _, t := range replay.Teams {
		if n := len(t.Samples); n > 0 {
			if s := t.Samples[n-1].Seconds(); s > max {
				max = s
			}
		}
	}
	return max
}

func decodeSample(b []byte) Sample {
	f := func(i int) float32 { return floatFrom(byteOrder.Uint32(b[i*4:])) }
	n := func(i int) int32 { return int32(byteOrder.Uint32(b[i*4:])) }
	return Sample{
		Frame:            n(0),
		MetalUsed:        f(1),
		EnergyUsed:       f(2),
		MetalProduced:    f(3),
		EnergyProduced:   f(4),
		MetalExcess:      f(5),
		EnergyExcess:     f(6),
		MetalReceived:    f(7),
		EnergyReceived:   f(8),
		MetalSent:        f(9),
		EnergySent:       f(10),
		DamageDealt:      f(11),
		DamageReceived:   f(12),
		UnitsProduced:    n(13),
		UnitsDied:        n(14),
		UnitsReceived:    n(15),
		UnitsSent:        n(16),
		UnitsCaptured:    n(17),
		UnitsOutCaptured: n(18),
		UnitsKilled:      n(19),
	}
}
