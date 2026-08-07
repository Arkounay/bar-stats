// Command inspect dumps a single replay to the terminal. It exists to debug
// the decoder against real files without going through the web UI.
//
// Usage: inspect <path-to-demo.sdfz>
package main

import (
	"fmt"
	"os"

	"barreplays/internal/demo"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: inspect <demo.sdfz>")
		os.Exit(2)
	}

	r, err := demo.Parse(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("file      %s\n", r.FileName)
	fmt.Printf("played    %s\n", r.Played.Format("2006-01-02 15:04"))
	fmt.Printf("engine    %s\n", r.Engine)
	fmt.Printf("map       %s\n", r.Map)
	fmt.Printf("duration  %s\n", r.Duration())
	fmt.Printf("hasStats  %v (period %ds)\n", r.HasStats, r.SamplePeriod)
	fmt.Printf("winners   ally teams %v\n", r.WinningAllyTeams)
	fmt.Printf("spawns    %d map start points\n", len(r.MapSpawns))
	if s := r.MapSize; s != nil {
		kind := "exact"
		if s.Approximate {
			kind = "approximate"
		}
		fmt.Printf("map size  %.0f x %.0f (%s)\n\n", s.Width, s.Height, kind)
	} else {
		fmt.Printf("map size  unknown\n\n")
	}

	for _, at := range r.AllyTeams {
		result := "lost"
		if at.Won {
			result = "WON"
		}
		fmt.Printf("Ally team %d (%s)\n", at.ID, result)
		for _, tid := range at.TeamIDs {
			t := r.TeamByID(tid)
			if t == nil {
				continue
			}
			kind := "human"
			if t.IsAI {
				kind = "AI"
			}
			var final demo.Sample
			if n := len(t.Samples); n > 0 {
				final = t.Samples[n-1]
			}
			fmt.Printf("  team %2d %-22s %-7s %-8s %5s  metal %10.0f  energy %12.0f  dmg %10.0f  units %4d/%-4d  %s\n",
				t.ID, t.Name, t.Side, kind, t.Color,
				final.MetalProduced, final.EnergyProduced, final.DamageDealt,
				final.UnitsProduced, final.UnitsDied, opening(t))
		}
	}

	fmt.Printf("\nplayers (%d)\n", len(r.Players))
	for _, p := range r.Players {
		fmt.Printf("  %-22s team %2d  cmds %6d  apm %5.1f  clicks %7d\n",
			p.Name, p.TeamID, p.Stats.NumCommands,
			p.Stats.APM(float64(r.DurationSeconds)), p.Stats.MouseClicks)
	}
	fmt.Printf("spectators %d\n", len(r.Spectators))
}

// opening formats a team's start position and opening factory, the two facts
// recovered from the packet stream rather than the statistics chunks.
func opening(t *demo.Team) string {
	start := "start ?"
	if p := t.StartPos; p != nil {
		start = fmt.Sprintf("start (%.0f,%.0f)", p.X, p.Z)
	}
	if f := t.FirstFactory; f != nil {
		when := fmt.Sprintf("%.0fs", f.Seconds())
		if f.PreGame() {
			when = "pre-game"
		}
		return fmt.Sprintf("%s  %s @ %s", start, f.Unit, when)
	}
	return start + "  no factory"
}
