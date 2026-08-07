package demo

import (
	"testing"
)

func TestApplyAutoColorsOverridesScriptColors(t *testing.T) {
	r := &Replay{Teams: []Team{
		{ID: 0, Color: "#fe8b00"},
		{ID: 1, Color: "#fe8b00"},
		{ID: 2, Color: "#123456"},
	}}
	applyAutoColors(r, []byte(autoColorsTag+
		`[{"teamID":0,"b":243,"g":62,"r":11},{"teamID":1,"b":5,"g":16,"r":255}]trailing`))

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
