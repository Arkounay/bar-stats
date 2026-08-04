// Package demo decodes Spring/Recoil demo files (.sdf) and their gzip-compressed
// form (.sdfz), as produced by Beyond All Reason.
//
// The on-disk layout, in order, is:
//
//	[0, 352)          fileHeader
//	[352, +ScriptSize)  start script (plain-text TDF)
//	                  demo stream    (raw network packets — skipped entirely)
//	                  winning ally teams (one byte per winning ally team)
//	                  player statistics  (PlayerStatElemSize bytes each)
//	                  team statistics    (int32 count per team, then samples)
//
// The trailing statistics chunks are written when recording finalises, so a
// match that was aborted (alt-F4, crash) has a valid header with zeroed
// counts. Such files are reported via [Replay.HasStats] rather than as errors.
package demo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Magic is the leading identifier of every Spring demo file.
const Magic = "spring demofile"

// SupportedVersion is the demo format version this package decodes.
//
// The format is versioned by the engine; refusing anything else is deliberate,
// as silently misreading a changed struct layout yields plausible-looking
// nonsense rather than an error.
const SupportedVersion = 5

// headerSize is the encoded size of fileHeader. The header carries its own
// size, which is cross-checked on decode: a mismatch means the engine changed
// the struct and the field offsets below are no longer trustworthy.
const headerSize = 352

// sampleSize is the encoded size of one team statistics sample.
const sampleSize = 80

// simFPS is the fixed simulation rate of the engine, used to convert the frame
// counter carried by each sample into wall-clock seconds.
const simFPS = 30

// ErrNotADemo reports a file that does not carry the demo magic.
var ErrNotADemo = errors.New("not a spring demo file")

// UnsupportedVersionError reports a demo whose format version this package
// does not decode.
type UnsupportedVersionError struct {
	Version int32
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("unsupported demo version %d (expected %d)", e.Version, SupportedVersion)
}

// fileHeader mirrors the engine's DemoFileHeader struct exactly. Field order
// and widths are load-bearing: encoding/binary decodes it as a flat little-endian
// record with no padding.
type fileHeader struct {
	Magic                [16]byte
	Version              int32
	HeaderSize           int32
	EngineVersion        [256]byte
	GameID               [16]byte
	UnixTime             int64
	ScriptSize           int32
	DemoStreamSize       int32
	GameTime             int32
	WallclockTime        int32
	NumPlayers           int32
	PlayerStatSize       int32
	PlayerStatElemSize   int32
	NumTeams             int32
	TeamStatSize         int32
	TeamStatElemSize     int32
	TeamStatPeriod       int32
	WinningAllyTeamsSize int32
}

// validate checks the invariants that make the rest of the decode safe.
func (h *fileHeader) validate() error {
	if trimZero(h.Magic[:]) != Magic {
		return ErrNotADemo
	}
	if h.Version != SupportedVersion {
		return &UnsupportedVersionError{Version: h.Version}
	}
	if h.HeaderSize != headerSize {
		return fmt.Errorf("header size %d does not match expected %d: engine struct layout changed", h.HeaderSize, headerSize)
	}
	if h.ScriptSize < 0 || h.DemoStreamSize < 0 || h.TeamStatSize < 0 || h.PlayerStatSize < 0 {
		return fmt.Errorf("negative chunk size in header")
	}
	// A zero element size would make the sample-count arithmetic divide by zero.
	if h.TeamStatSize > 0 && h.TeamStatElemSize != sampleSize {
		return fmt.Errorf("team stat element size %d does not match expected %d", h.TeamStatElemSize, sampleSize)
	}
	return nil
}

var byteOrder = binary.LittleEndian

// floatFrom reinterprets raw little-endian bits as the IEEE-754 float the
// engine wrote.
func floatFrom(bits uint32) float32 { return math.Float32frombits(bits) }

// trimZero converts a fixed-width C string field to a Go string, dropping the
// NUL terminator and anything after it.
func trimZero(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
