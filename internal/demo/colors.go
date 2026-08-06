package demo

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// autoColorsTag prefixes the LuaRules message Beyond All Reason broadcasts to
// publish the colours it picked for each team.
const autoColorsTag = "AutoColors"

// applyAutoColors overwrites the script's team colours with the ones the game
// actually used, if the demo stream carries them.
//
// Beyond All Reason ignores the rgbcolor the lobby writes into the start
// script — matchmaking hands every team the same placeholder — and assigns its
// own from a palette as the match loads (luarules/gadgets/game_autocolors.lua).
// That gadget broadcasts the result as a LuaRules message for the express
// purpose of letting replay readers recover it, so this, not the script, is
// what the player saw on screen.
//
// The message is located by its tag rather than by decoding the packet stream:
// the payload is self-describing JSON, so a mis-hit fails to decode rather than
// yielding plausible nonsense.
func applyAutoColors(r *Replay, stream []byte) {
	i := bytes.Index(stream, []byte(autoColorsTag+"["))
	if i < 0 {
		return
	}
	payload := stream[i+len(autoColorsTag):]
	end := bytes.IndexByte(payload, ']')
	if end < 0 {
		return
	}

	var entries []struct {
		TeamID int `json:"teamID"`
		R      int `json:"r"`
		G      int `json:"g"`
		B      int `json:"b"`
	}
	if err := json.Unmarshal(payload[:end+1], &entries); err != nil {
		return
	}
	for _, e := range entries {
		if t := r.TeamByID(e.TeamID); t != nil {
			t.Color = hexColor(e.R, e.G, e.B)
		}
	}
}

// rgbHex converts Spring's "0.65098 0.69020 0.40392" float triple to a CSS hex
// colour. It returns "" when the value is absent or malformed.
func rgbHex(v string) string {
	parts := strings.Fields(v)
	if len(parts) < 3 {
		return ""
	}
	var c [3]int
	for i, p := range parts[:3] {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return ""
		}
		c[i] = int(f*255 + 0.5)
	}
	return hexColor(c[0], c[1], c[2])
}

// hexColor formats an 8-bit RGB triple as a CSS colour. Components are clamped
// rather than trusted: the game's palette arithmetic brightens and jitters its
// base colours without bounding the result, so a team can be handed a
// component above 255.
func hexColor(r, g, b int) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 7)
	out = append(out, '#')
	for _, n := range [3]int{r, g, b} {
		if n < 0 {
			n = 0
		} else if n > 255 {
			n = 255
		}
		out = append(out, hexDigits[n>>4], hexDigits[n&0x0f])
	}
	return string(out)
}
