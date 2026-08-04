package demo

// Sample is one team statistics record, captured by the engine every
// [Replay.SamplePeriod] seconds. Field order and widths mirror the engine's
// CTeam::Statistics struct and must not be reordered — the binary decode
// depends on them.
//
// Every value except Frame is a running total accumulated since the start of
// the match, which is why [Metric.Cumulative] is true for all of them: the
// per-interval rate is derived by differencing consecutive samples.
type Sample struct {
	Frame int32 // simulation frame this sample was taken at

	MetalUsed      float32
	EnergyUsed     float32
	MetalProduced  float32
	EnergyProduced float32
	MetalExcess    float32
	EnergyExcess   float32
	MetalReceived  float32
	EnergyReceived float32
	MetalSent      float32
	EnergySent     float32
	DamageDealt    float32
	DamageReceived float32

	UnitsProduced    int32
	UnitsDied        int32
	UnitsReceived    int32
	UnitsSent        int32
	UnitsCaptured    int32
	UnitsOutCaptured int32
	UnitsKilled      int32
}

// Seconds returns the match time this sample was taken at.
func (s Sample) Seconds() float64 { return float64(s.Frame) / simFPS }

// MetricGroup buckets metrics for presentation. The UI renders one section per
// group, in the order the groups are declared in [MetricGroups].
type MetricGroup string

const (
	GroupEconomy  MetricGroup = "economy"
	GroupMilitary MetricGroup = "military"
	GroupUnits    MetricGroup = "units"
)

// MetricGroups is the display order of the groups.
var MetricGroups = []MetricGroup{GroupEconomy, GroupMilitary, GroupUnits}

// Metric describes one plottable quantity carried by a [Sample].
//
// This registry is the single place that knows the set of available statistics:
// the HTTP API publishes it, the frontend builds its chart list from it, and
// the series extraction reads values through it. Exposing a new statistic is
// therefore one struct field plus one entry here — no changes anywhere else.
type Metric struct {
	Key   string      `json:"key"`
	Label string      `json:"label"`
	Group MetricGroup `json:"group"`
	// Unit labels the y-axis. Empty means a bare count, which is also what
	// tells the UI to show the value unabbreviated.
	Unit string `json:"unit"`
	// Cumulative marks a running total, for which the UI offers a derived
	// per-minute rate in addition to the raw total.
	Cumulative bool `json:"cumulative"`
	// Roster gives the metric a column in the per-team table, headed by
	// Short. Declaring it here rather than in the frontend is what keeps
	// "one field plus one registry entry" true.
	Roster bool `json:"roster,omitempty"`
	// Short is the column heading for Roster metrics; Label is used if empty.
	Short string `json:"short,omitempty"`
	// Headline gives the metric a summary tile above the charts, totalled
	// across every team.
	Headline bool `json:"headline,omitempty"`
	// Value extracts the metric from a sample. Not serialised.
	Value func(Sample) float64 `json:"-"`
}

// Metrics is the ordered registry of everything a demo records per team.
var Metrics = []Metric{
	{Key: "metalProduced", Label: "Metal produced", Group: GroupEconomy, Unit: "metal", Cumulative: true,
		Roster: true, Short: "Metal",
		Value:  func(s Sample) float64 { return float64(s.MetalProduced) }},
	{Key: "metalUsed", Label: "Metal used", Group: GroupEconomy, Unit: "metal", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.MetalUsed) }},
	{Key: "metalExcess", Label: "Metal excess", Group: GroupEconomy, Unit: "metal", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.MetalExcess) }},
	{Key: "energyProduced", Label: "Energy produced", Group: GroupEconomy, Unit: "energy", Cumulative: true,
		Roster: true, Short: "Energy",
		Value:  func(s Sample) float64 { return float64(s.EnergyProduced) }},
	{Key: "energyUsed", Label: "Energy used", Group: GroupEconomy, Unit: "energy", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.EnergyUsed) }},
	{Key: "energyExcess", Label: "Energy excess", Group: GroupEconomy, Unit: "energy", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.EnergyExcess) }},

	{Key: "damageDealt", Label: "Damage dealt", Group: GroupMilitary, Unit: "damage", Cumulative: true,
		Roster: true, Short: "Damage dealt", Headline: true,
		Value:  func(s Sample) float64 { return float64(s.DamageDealt) }},
	{Key: "damageReceived", Label: "Damage received", Group: GroupMilitary, Unit: "damage", Cumulative: true,
		Roster: true, Short: "Damage taken",
		Value:  func(s Sample) float64 { return float64(s.DamageReceived) }},
	{Key: "unitsKilled", Label: "Units killed", Group: GroupMilitary, Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.UnitsKilled) }},

	{Key: "unitsProduced", Label: "Units produced", Group: GroupUnits, Cumulative: true,
		Roster: true, Short: "Units made", Headline: true,
		Value:  func(s Sample) float64 { return float64(s.UnitsProduced) }},
	{Key: "unitsDied", Label: "Units lost", Group: GroupUnits, Cumulative: true,
		Roster: true, Short: "Units lost",
		Value:  func(s Sample) float64 { return float64(s.UnitsDied) }},

	{Key: "metalSent", Label: "Metal sent", Group: GroupEconomy, Unit: "metal", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.MetalSent) }},
	{Key: "metalReceived", Label: "Metal received", Group: GroupEconomy, Unit: "metal", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.MetalReceived) }},
	{Key: "energySent", Label: "Energy sent", Group: GroupEconomy, Unit: "energy", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.EnergySent) }},
	{Key: "energyReceived", Label: "Energy received", Group: GroupEconomy, Unit: "energy", Cumulative: true,
		Value: func(s Sample) float64 { return float64(s.EnergyReceived) }},
}

// MetricByKey looks up a registered metric.
func MetricByKey(key string) (Metric, bool) {
	for _, m := range Metrics {
		if m.Key == key {
			return m, true
		}
	}
	return Metric{}, false
}

// PlayerStats is the per-player input activity the engine records. Unlike team
// statistics these are totals for the whole match, not a time series.
type PlayerStats struct {
	MousePixels  int32 `json:"mousePixels"`
	MouseClicks  int32 `json:"mouseClicks"`
	KeyPresses   int32 `json:"keyPresses"`
	NumCommands  int32 `json:"numCommands"`
	UnitCommands int32 `json:"unitCommands"`
}

// APM returns actions per minute over a match of the given duration, the
// conventional RTS activity measure. It returns 0 for a zero-length match.
func (p PlayerStats) APM(matchSeconds float64) float64 {
	if matchSeconds <= 0 {
		return 0
	}
	return float64(p.NumCommands) / (matchSeconds / 60)
}
