package demo

import (
	"bytes"
	"io"
	"testing"
)

// autoColorsPacket is the LuaRules message as it appears in a real demo,
// preceded by the packet framing the search has to tolerate.
const autoColorsPacket = "2|\x00\x00d\x00\x00AutoColors" +
	`[{"teamID":0,"b":243,"g":62,"r":11},{"teamID":1,"b":5,"g":16,"r":255}]`

func TestApplyAutoColorsOverridesScriptColors(t *testing.T) {
	r := &Replay{Teams: []Team{
		{ID: 0, Color: "#fe8b00"},
		{ID: 1, Color: "#fe8b00"},
		{ID: 2, Color: "#123456"},
	}}
	applyAutoColors(r, []byte("noise\x00\xab"+autoColorsPacket+"trailing"))

	for i, want := range []string{"#0b3ef3", "#ff1005", "#123456"} {
		if got := r.Teams[i].Color; got != want {
			t.Errorf("team %d colour = %q, want %q", i, got, want)
		}
	}
}

func TestApplyAutoColorsLeavesScriptColorsAlone(t *testing.T) {
	cases := map[string]string{
		"absent":     "just some packets",
		"truncated":  `AutoColors[{"teamID":0,"r":1,`,
		"not json":   "AutoColors[nonsense]",
		"wrong tag":  `AutoColorsX[{"teamID":0,"r":1,"g":2,"b":3}]`,
		"clamps out": `AutoColors[{"teamID":0,"r":300,"g":-20,"b":128}]`,
	}
	want := map[string]string{"clamps out": "#ff0080"}

	for name, stream := range cases {
		r := &Replay{Teams: []Team{{ID: 0, Color: "#abcdef"}}}
		applyAutoColors(r, []byte(stream))
		expected := "#abcdef"
		if w, ok := want[name]; ok {
			expected = w
		}
		if got := r.Teams[0].Color; got != expected {
			t.Errorf("%s: colour = %q, want %q", name, got, expected)
		}
	}
}

// TestReadDemoStreamConsumesExactly guards the invariant the trailing chunks
// depend on: whatever this reads for colours, the reader must be left at the
// first byte after the stream.
func TestReadDemoStreamConsumesExactly(t *testing.T) {
	for _, size := range []int{0, 64, colorScanBytes + 1024} {
		stream := bytes.Repeat([]byte{0xAB}, size)
		if size > len(autoColorsPacket) {
			copy(stream, autoColorsPacket)
		}
		src := bytes.NewReader(append(stream, []byte("TRAILER")...))

		r := &Replay{Teams: []Team{{ID: 0, Color: "#fe8b00"}}}
		if err := readDemoStream(src, int64(size), r); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}

		rest, err := io.ReadAll(src)
		if err != nil {
			t.Fatal(err)
		}
		if string(rest) != "TRAILER" {
			t.Errorf("size %d: left %q after the stream, want %q", size, rest, "TRAILER")
		}
		if size > len(autoColorsPacket) && r.Teams[0].Color != "#0b3ef3" {
			t.Errorf("size %d: colour = %q, want #0b3ef3", size, r.Teams[0].Color)
		}
	}
}
