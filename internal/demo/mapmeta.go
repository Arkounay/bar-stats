package demo

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
)

// startPosModOption is the lobby-set modoption carrying the map's spawn point
// metadata.
//
// Beyond All Reason's lobby ships the map's own list of start points with the
// match, so the game can lay teams out and draft positions. It is stored as
// URL-safe base64 wrapping zlib-compressed JSON, and it is the only statement
// of where on a map players may stand that travels inside a replay.
const startPosModOption = "mapmetadata_startpos"

// maxInflatedSize bounds decompressed Lua payloads. They are a few kilobytes
// in practice; the limit stops a malformed one inflating unbounded.
const maxInflatedSize = 1 << 20

// inflateJSON decodes zlib-compressed JSON into v.
//
// Beyond All Reason wraps both the payloads read out of a replay this way —
// the map's spawn points in the start script, and the unit name table on the
// wire — so the size cap and the decode live here rather than at each.
func inflateJSON(compressed []byte, v any) error {
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	defer zr.Close()

	data, err := io.ReadAll(io.LimitReader(zr, maxInflatedSize))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// applyMapSpawns decodes the map's spawn points from the start script's
// modoptions.
//
// The points matter because a start position on its own is a bare coordinate:
// nothing else in a replay says how large the map is, so there is no way to
// tell whether x=3400 is a corner or the middle. The spawn list spans the
// playable area and gives those coordinates a frame of reference.
func applyMapSpawns(r *Replay, game *tdfSection) {
	encoded := game.child("modoptions").str(startPosModOption)
	if encoded == "" {
		return
	}
	// The lobby writes unpadded URL-safe base64.
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}

	// Positions are keyed by spawn point name ("P1", "P2", …). The metadata's
	// y is the map's second horizontal axis — the engine's z — not a height.
	var meta struct {
		Positions map[string]struct {
			X float32 `json:"x"`
			Y float32 `json:"y"`
		} `json:"positions"`
	}
	if err := inflateJSON(raw, &meta); err != nil {
		return
	}
	for _, p := range meta.Positions {
		r.MapSpawns = append(r.MapSpawns, Position{X: p.X, Z: p.Y})
	}
}

// mapGrid is the granularity of a Spring map's dimensions in world units. The
// engine sizes a map in heightmap squares, which must come in multiples of
// 128, each 8 units across — so every map's width and height is a whole
// multiple of 1024.
const mapGrid = 1024

// maxGridResidual is how far an estimate may sit from a whole grid step and
// still be rounded onto it, as a fraction of one step. Within this, rounding
// corrects the estimate; beyond it, rounding would be a coin toss between two
// sizes and the estimate is kept as measured instead.
const maxGridResidual = 0.25

// minStartsForSize is how many start positions are needed before the players'
// own layout is taken as evidence of the map's extent, when the map declared
// no spawn points of its own.
const minStartsForSize = 4

// estimateMapSize works out a map's dimensions from the positions recorded in
// a replay.
//
// Nothing in a replay states how large a map is, yet without that a start
// position cannot be placed on a picture of one — 3400 could be a corner or
// the middle. Symmetry gives it away: maps are laid out symmetrically, so the
// lowest and highest position sit equal distances from opposite edges and
// their sum is the map's extent.
//
// The map's own declared spawn points are used where the lobby recorded them,
// since they are placed deliberately and mirror exactly. Failing that the
// players' own start positions stand in, which is rougher — a side that all
// started in one corner understates the map — but still yields a usable
// picture, and [MapSize.Approximate] says which case produced it.
func estimateMapSize(r *Replay) *MapSize {
	points := r.MapSpawns
	// Declared spawn points are the better evidence; fall back to where the
	// players actually stood.
	exact := len(points) >= 2
	if !exact {
		for _, t := range r.Teams {
			if t.StartPos != nil {
				points = append(points, *t.StartPos)
			}
		}
		// A handful of players is not a symmetric layout. Two of them can sit
		// anywhere at all — a 1v1 where both picked the same side of the map
		// implies a map barely wider than the gap between them, which would
		// put every marker in the wrong place. A full team game spreads out
		// enough for the symmetry to mean something.
		if len(points) < minStartsForSize {
			return nil
		}
	}

	minX, maxX := points[0].X, points[0].X
	minZ, maxZ := points[0].Z, points[0].Z
	for _, p := range points[1:] {
		minX, maxX = min(minX, p.X), max(maxX, p.X)
		minZ, maxZ = min(minZ, p.Z), max(maxZ, p.Z)
	}

	width, okW := snapToGrid(minX+maxX, maxX)
	height, okH := snapToGrid(minZ+maxZ, maxZ)
	return &MapSize{
		Width:  width,
		Height: height,
		// Only an estimate off the grid, or one taken from the players rather
		// than the map, is called approximate. A clean snap from declared
		// spawn points is the map's real size.
		Approximate: !exact || !okW || !okH,
	}
}

// snapToGrid rounds an estimated extent onto the map grid, reporting whether
// it landed close enough to a step to be taken as exact.
//
// The rounding is applied either way. Every real map is a whole number of grid
// steps, so a rounded figure is always the more likely of the two — keeping
// the raw measurement would guarantee a size no map can have, while rounding
// is wrong by at most one step even at its worst. The flag records confidence,
// not whether to round.
//
// atLeast is the furthest-out position seen. The map has to contain it, so it
// is a floor no estimate may fall below — that alone keeps a bad estimate from
// pushing markers off the edge of the picture.
func snapToGrid(estimate, atLeast float32) (float32, bool) {
	steps := float64(estimate) / mapGrid
	rounded := math.Max(1, math.Round(steps))
	ok := math.Abs(steps-rounded) <= maxGridResidual

	size := float32(rounded * mapGrid)
	floor := float32(math.Ceil(float64(atLeast)/mapGrid) * mapGrid)
	if size < floor {
		return floor, false
	}
	return size, ok
}
