package demo

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// This file decodes the demo stream — the middle section of a demo file, and
// the only record of what players actually did.
//
// The stream is a sequence of the network packets the engine broadcast during
// the match, each wrapped in an 8-byte chunk header:
//
//	float32 modGameTime   // seconds since recording began, not game time
//	uint32  length        // bytes of packet payload following
//	byte    payload[length]
//
// The first payload byte is the message ID. That framing is what makes the
// stream walkable at all: a packet whose internal layout this file does not
// know can still be skipped exactly, so an unrecognised or changed message
// costs its own contents and nothing else.
//
// The stream carries no world state. There is no unit list, no positions over
// time, no record of what was built — only player input plus the messages the
// game's Lua layer broadcasts. Everything below is recovered from those.

// Network message IDs, from the engine's netprotocol. Only the messages read
// here are named; the rest are skipped by length.
const (
	msgKeyFrame  = 1  // int32 frame number
	msgNewFrame  = 2  // advances the simulation one frame
	msgCommand   = 11 // an order given to the issuing player's selection
	msgAICommand = 14 // an order given to one named unit
	msgStartPos  = 36 // a team's chosen start position
	msgLuaMsg    = 50 // a broadcast from the game's Lua layer
)

// chunkHeaderSize is the per-packet framing described above.
const chunkHeaderSize = 8

// maxChunkSize bounds a declared packet length before it is trusted. Real
// packets are at most a few kilobytes; anything larger means the framing has
// been lost, and allocating on the declared length would turn that into an
// out-of-memory rather than an abandoned walk.
const maxChunkSize = 1 << 20

// luaMsgHeaderSize is the fixed part of a LUAMSG packet — message ID, size,
// player, script ID and mode — before its payload.
const luaMsgHeaderSize = 7

// unitDefsTag prefixes the Lua broadcast carrying the match's unit name table.
//
// Beyond All Reason publishes it so replay readers can resolve the unit def
// IDs that appear in build orders, which are otherwise just numbers whose
// meaning changes with the game version. The payload is zlib-compressed JSON:
// an array of every unit name in def order.
const unitDefsTag = "unitdefs:"

// readBufferSize is how much of the stream is read from the decompressor at a
// time. Packets are mostly a few bytes, and reading each one straight from the
// gzip reader costs a checksum update per call.
const readBufferSize = 1 << 16

// readDemoStream consumes the demo stream, extracting the setup facts that the
// trailing statistics chunks do not carry: the colours the game assigned, where
// each team started, and the first factory each ordered.
//
// The stream has to be decompressed to reach the statistics behind it either
// way, so walking it is a fraction of the cost already being paid for the gzip.
//
// A stream whose framing does not hold up is not an error. Recordings that were
// cut off mid-packet are common, and the statistics chunks behind the stream
// are still readable, so the walk stops and keeps whatever it had.
func readDemoStream(r io.Reader, size int64, replay *Replay) error {
	stream := io.LimitReader(r, size)
	w := &streamWalker{
		replay:       replay,
		firstFactory: map[int]BuildOrder{},
		startPos:     map[int]Position{},
	}
	w.walk(bufio.NewReaderSize(stream, readBufferSize))
	w.apply()

	// However far the walk got, the reader has to end up at the first byte
	// after the stream or every trailing chunk decodes as garbage. Buffering
	// above reads ahead, but only ever out of this same limited reader, so
	// draining it lands exactly at the end either way.
	if _, err := io.Copy(io.Discard, stream); err != nil {
		return fmt.Errorf("read demo stream: %w", err)
	}
	return nil
}

// streamWalker accumulates what one pass over the packet stream reveals.
type streamWalker struct {
	replay *Replay

	// frame tracks the simulation clock. Every frame the engine advances is
	// announced by exactly one NEWFRAME or KEYFRAME, so counting them gives the
	// same game time the match itself ran on — independent of how long players
	// sat in the lobby beforehand, which is what the chunk header's
	// modGameTime measures.
	frame int32

	// unitNames maps a unit def ID to its internal name, one-based: def ID 1 is
	// the first entry. Nil until the game broadcasts the table.
	unitNames []string

	// firstFactory is keyed by team ID. Build orders name a player, so a team
	// nobody played never gets an entry.
	firstFactory map[int]BuildOrder
	startPos     map[int]Position

	// teamOfPlayer maps a network player number to the team it commanded,
	// built once rather than scanned per packet.
	playerTeams map[int]int
}

func (w *streamWalker) walk(r io.Reader) {
	header := make([]byte, chunkHeaderSize)
	// One buffer for every packet: nothing here keeps a payload past the call
	// that decodes it, and a demo holds hundreds of thousands of packets.
	var payload []byte
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			return
		}
		n := int(byteOrder.Uint32(header[4:]))
		if n <= 0 || n > maxChunkSize {
			return // framing lost; keep what we have
		}
		if cap(payload) < n {
			payload = make([]byte, n)
		}
		if _, err := io.ReadFull(r, payload[:n]); err != nil {
			return
		}
		w.packet(payload[:n])
	}
}

func (w *streamWalker) packet(b []byte) {
	switch b[0] {
	case msgNewFrame:
		w.frame++
	case msgKeyFrame:
		if len(b) >= 5 {
			w.frame = int32(byteOrder.Uint32(b[1:]))
		}
	case msgStartPos:
		w.startPosition(b)
	case msgLuaMsg:
		w.luaMessage(b)
	case msgCommand:
		// uchar id; uint16 size; uchar player; int32 cmdID; …
		w.command(b, 4)
	case msgAICommand:
		// As above, with an AI ID and the target unit ID before the command.
		w.command(b, 8)
	}
}

// startPosition records a team's chosen start position. The message is resent
// as a player drags their marker around, so the last one seen is the one they
// settled on.
func (w *streamWalker) startPosition(b []byte) {
	// uchar id; uchar player; uchar team; uchar readyState; float x, y, z
	const size = 16
	if len(b) < size {
		return
	}
	w.startPos[int(b[2])] = Position{
		X: floatFrom(byteOrder.Uint32(b[4:])),
		Z: floatFrom(byteOrder.Uint32(b[12:])),
	}
}

// luaMessage dispatches the broadcasts the game's own Lua layer makes, by the
// tag each one opens with.
//
// Both facts recovered here are published by Beyond All Reason for the express
// purpose of being read back out of a replay, so a game that did not send them
// simply yields neither.
func (w *streamWalker) luaMessage(b []byte) {
	if len(b) <= luaMsgHeaderSize {
		return
	}
	payload := b[luaMsgHeaderSize:]
	switch {
	case bytes.HasPrefix(payload, []byte(unitDefsTag)):
		if w.unitNames == nil {
			w.unitNames = decodeUnitNames(payload[len(unitDefsTag):])
		}
	case bytes.HasPrefix(payload, []byte(autoColorsTag)):
		applyAutoColors(w.replay, payload)
	}
}

// decodeUnitNames inflates the unit name table. It returns nil for a payload
// that does not decode, leaving the table unset as if it had never been sent.
func decodeUnitNames(compressed []byte) []string {
	var names []string
	if err := inflateJSON(compressed, &names); err != nil {
		return nil
	}
	return names
}

// command records a build order. cmdOff is where the command ID sits, the only
// thing that differs between the two command messages; every field after it
// follows at a fixed distance.
//
// A negative command ID is the engine's encoding of "build this": its
// magnitude is the unit def ID. Positive IDs are ordinary orders — move,
// attack, reclaim — and are not recorded.
func (w *streamWalker) command(b []byte, cmdOff int) {
	// uchar player; int32 cmdID; uchar options; uint32 timeout;
	// uint32 numParams; float params[numParams]
	const playerOff = 3
	if len(b) < cmdOff+4 {
		return
	}
	cmdID := int32(byteOrder.Uint32(b[cmdOff:]))
	if cmdID >= 0 {
		return
	}
	unit := w.unitName(-cmdID)
	if unit == "" || !isFactory(unit) {
		return
	}
	team, ok := w.teamOfPlayer(int(b[playerOff]))
	if !ok {
		return
	}
	if _, seen := w.firstFactory[team]; !seen {
		w.firstFactory[team] = BuildOrder{Unit: unit, Kind: factoryKind(unit), Frame: w.frame}
	}
}

// unitName resolves a unit def ID against the broadcast name table. Def IDs are
// one-based.
func (w *streamWalker) unitName(defID int32) string {
	i := int(defID) - 1
	if i < 0 || i >= len(w.unitNames) {
		return ""
	}
	return w.unitNames[i]
}

// teamOfPlayer maps a network player number to the team it commanded.
// Spectators and unknown players report no team.
func (w *streamWalker) teamOfPlayer(id int) (int, bool) {
	if w.playerTeams == nil {
		w.playerTeams = make(map[int]int, len(w.replay.Players))
		for _, p := range w.replay.Players {
			if !p.Spectator && p.TeamID >= 0 {
				w.playerTeams[p.ID] = p.TeamID
			}
		}
	}
	team, ok := w.playerTeams[id]
	return team, ok
}

// apply writes what the walk found onto the replay.
func (w *streamWalker) apply() {
	for i := range w.replay.Teams {
		t := &w.replay.Teams[i]
		if pos, ok := w.startPos[t.ID]; ok {
			t.StartPos = &pos
		}
		if order, ok := w.firstFactory[t.ID]; ok {
			t.FirstFactory = &order
		}
	}
}

// factoryKinds gives the kind of factory a unit name denotes, in the words
// players use for them: bot lab, vehicle plant, aircraft plant, shipyard,
// hovercraft platform, gantry, amphibious complex. Every faction and tech
// level follows the same endings, so this covers variants — armalab, corgant,
// legvp — without listing each one.
//
// Order is load-bearing: the short endings are endings of the long ones, and
// "corgant" finishes with "ap" as surely as "corap" does. Longest first.
//
// Tier is deliberately left out. Telling armap from armaap takes a second
// guess, and an opening factory is a tier 1 building in all but a handful of
// matches.
var factoryKinds = []struct{ suffix, kind string }{
	{"amphlab", "amphib"},
	{"amsub", "amphib"},
	{"shltx", "gantry"},
	{"gant", "gantry"},
	{"seap", "seaplane"},
	{"reap", "seaplane"},
	{"fhp", "hover"},
	{"lab", "bot"},
	{"hp", "hover"},
	{"vp", "vehicle"},
	{"ap", "air"},
	{"sy", "sea"},
}

// factoryExclusions are names that end like a factory but are not one. Nano
// turrets and the naval platforms they stand on share the endings above.
var factoryExclusions = []string{"nanotc", "plat"}

// factoryKind names the kind of factory a unit is, or "" if it is not one.
//
// This is a naming heuristic, not a property read from the game files: the
// demo carries unit names but no unit definitions, and reading definitions
// would mean loading the matching version of the game archive for every
// replay. Checked against a folder of real matches it picks out exactly the
// factory set and nothing else.
func factoryKind(name string) string {
	for _, ex := range factoryExclusions {
		if strings.Contains(name, ex) {
			return ""
		}
	}
	for _, f := range factoryKinds {
		if strings.HasSuffix(name, f.suffix) {
			return f.kind
		}
	}
	return ""
}

// isFactory reports whether a unit name is one of the game's factories.
func isFactory(name string) bool { return factoryKind(name) != "" }
