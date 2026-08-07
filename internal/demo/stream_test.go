package demo

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"io"

	"testing"
)

// put appends fixed-width little-endian values, the encoding every packet
// below is built from.
func put(w *bytes.Buffer, values ...any) {
	for _, v := range values {
		if err := binary.Write(w, byteOrder, v); err != nil {
			panic(err) // only a bad value type reaches here, in this file's own code
		}
	}
}

// chunk wraps a packet in the demo stream's framing.
func chunk(modGameTime float32, payload []byte) []byte {
	var out bytes.Buffer
	put(&out, modGameTime, uint32(len(payload)))
	out.Write(payload)
	return out.Bytes()
}

// startPosPacket builds a NETMSG_STARTPOS.
func startPosPacket(player, team byte, x, y, z float32) []byte {
	var b bytes.Buffer
	put(&b, byte(msgStartPos), player, team, byte(1) /* ready */, x, y, z)
	return b.Bytes()
}

// commandPacket builds a NETMSG_COMMAND. A negative cmdID is a build order and
// its magnitude is the unit def ID.
func commandPacket(player byte, cmdID int32, params []float32) []byte {
	var b bytes.Buffer
	put(&b, byte(msgCommand), uint16(17+4*len(params)), player, cmdID,
		byte(0xff) /* options */, uint32(0) /* timeout */, uint32(len(params)))
	for _, p := range params {
		put(&b, p)
	}
	return b.Bytes()
}

// luaPacket builds a LUAMSG carrying the given payload.
func luaPacket(payload []byte) []byte {
	var b bytes.Buffer
	// message ID, size (unread), player, script, mode
	put(&b, byte(msgLuaMsg), uint16(0), byte(0), uint16(0), byte(0))
	b.Write(payload)
	return b.Bytes()
}

// unitDefsPacket builds the LUAMSG carrying the compressed unit name table.
func unitDefsPacket(t *testing.T, names []string) []byte {
	t.Helper()
	raw, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return luaPacket(append([]byte(unitDefsTag), z.Bytes()...))
}

func newFramePacket() []byte { return []byte{msgNewFrame} }

// testReplay is a two-team match whose players match the packets below.
func testReplay() *Replay {
	return &Replay{
		Teams: []Team{{ID: 0}, {ID: 1}},
		Players: []Player{
			{ID: 0, Name: "Alice", TeamID: 0},
			{ID: 1, Name: "Bob", TeamID: 1},
			{ID: 2, Name: "Watcher", TeamID: -1, Spectator: true},
		},
	}
}

// TestReadDemoStreamExtractsOpening covers the whole packet walk: the frame
// clock, the unit name table, start positions and the first factory per team.
func TestReadDemoStreamExtractsOpening(t *testing.T) {
	// Def IDs are one-based, so armlab is 2 and corvp is 4.
	names := []string{"armsolar", "armlab", "corwin", "corvp", "armnanotcplat"}

	var stream bytes.Buffer
	stream.Write(chunk(10, unitDefsPacket(t, names)))
	// The colours the game assigned arrive the same way as the name table.
	stream.Write(chunk(10, luaPacket([]byte(autoColorsTag+
		`[{"teamID":0,"r":11,"g":62,"b":243}]`))))
	stream.Write(chunk(11, startPosPacket(0, 0, 100, 5, 200)))
	// Resent as the player drags the marker; the last one is the real choice.
	stream.Write(chunk(12, startPosPacket(0, 0, 111, 5, 222)))
	stream.Write(chunk(12, startPosPacket(1, 1, 900, 7, 800)))

	// Alice pre-queues a lab before the match starts.
	stream.Write(chunk(13, commandPacket(0, -2, []float32{111, 5, 230, 1})))
	// Two minutes of simulation.
	for i := 0; i < 60*simFPS; i++ {
		stream.Write(chunk(14, newFramePacket()))
	}
	// Bob orders a vehicle plant a minute in, and a lab afterwards which must
	// not displace it.
	stream.Write(chunk(80, commandPacket(1, -4, []float32{900, 7, 810, 0})))
	stream.Write(chunk(81, commandPacket(1, -2, []float32{900, 7, 820, 0})))
	// A nano turret is not a factory, and a spectator commands no team.
	stream.Write(chunk(82, commandPacket(0, -5, []float32{1, 2, 3, 0})))
	stream.Write(chunk(83, commandPacket(2, -2, []float32{1, 2, 3, 0})))

	body := append(stream.Bytes(), []byte("TRAILER")...)
	src := bytes.NewReader(body)
	r := testReplay()
	if err := readDemoStream(src, int64(stream.Len()), r); err != nil {
		t.Fatalf("readDemoStream: %v", err)
	}

	rest, err := io.ReadAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "TRAILER" {
		t.Errorf("reader left at %q, want %q", rest, "TRAILER")
	}

	if got := r.Teams[0].Color; got != "#0b3ef3" {
		t.Errorf("team 0 colour = %q, want the broadcast #0b3ef3", got)
	}
	if p := r.Teams[0].StartPos; p == nil || p.X != 111 || p.Z != 222 {
		t.Errorf("team 0 start = %+v, want the last broadcast (111, 222)", p)
	}
	if p := r.Teams[1].StartPos; p == nil || p.X != 900 || p.Z != 800 {
		t.Errorf("team 1 start = %+v, want (900, 800)", p)
	}

	alice := r.Teams[0].FirstFactory
	if alice == nil {
		t.Fatal("team 0 has no first factory")
	}
	if alice.Unit != "armlab" {
		t.Errorf("team 0 factory = %q, want armlab", alice.Unit)
	}
	if !alice.PreGame() {
		t.Errorf("team 0 factory at frame %d should count as pre-game", alice.Frame)
	}
	if alice.Kind != "bot" {
		t.Errorf("team 0 factory kind = %q, want bot", alice.Kind)
	}

	bob := r.Teams[1].FirstFactory
	if bob == nil {
		t.Fatal("team 1 has no first factory")
	}
	if bob.Unit != "corvp" {
		t.Errorf("team 1 factory = %q, want corvp (the first, not the latest)", bob.Unit)
	}
	if bob.PreGame() {
		t.Error("team 1 factory was ordered a minute in, not pre-game")
	}
	if got := bob.Seconds(); got < 59 || got > 61 {
		t.Errorf("team 1 factory at %.1fs, want about 60", got)
	}
}

// TestReadDemoStreamSurvivesGarbage covers a recording cut off mid-packet: the
// walk must abandon the stream rather than fail, because the statistics behind
// it are still readable.
func TestReadDemoStreamSurvivesGarbage(t *testing.T) {
	for _, size := range []int{0, 64, 1 << 20} {
		stream := bytes.Repeat([]byte{0xAB}, size)
		src := bytes.NewReader(append(stream, []byte("TRAILER")...))

		r := testReplay()
		if err := readDemoStream(src, int64(size), r); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		rest, err := io.ReadAll(src)
		if err != nil {
			t.Fatal(err)
		}
		if string(rest) != "TRAILER" {
			t.Errorf("size %d: left %q, want TRAILER", size, rest)
		}
		if r.Teams[0].StartPos != nil || r.Teams[0].FirstFactory != nil {
			t.Errorf("size %d: garbage produced an opening", size)
		}
	}
}

// TestReadDemoStreamWithoutNameTable covers a replay whose game never
// broadcast the unit names: build orders are unresolvable, so no factory is
// reported rather than a wrong one.
func TestReadDemoStreamWithoutNameTable(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(chunk(10, startPosPacket(0, 0, 100, 5, 200)))
	stream.Write(chunk(11, commandPacket(0, -2, []float32{100, 5, 210, 0})))

	r := testReplay()
	if err := readDemoStream(bytes.NewReader(stream.Bytes()), int64(stream.Len()), r); err != nil {
		t.Fatal(err)
	}
	if r.Teams[0].StartPos == nil {
		t.Error("start position should decode without the name table")
	}
	if f := r.Teams[0].FirstFactory; f != nil {
		t.Errorf("factory = %+v, want none without a name table", f)
	}
}

func TestIsFactory(t *testing.T) {
	for _, name := range []string{
		"armlab", "corlab", "leglab", "armvp", "corvp", "armap", "corap",
		"armsy", "corsy", "armhp", "corhp", "armalab", "coravp", "corgant",
		"armshltx", "armamsub",
	} {
		if !isFactory(name) {
			t.Errorf("isFactory(%q) = false, want true", name)
		}
	}
	// Nano turrets and the platforms they sit on end like factories.
	for _, name := range []string{
		"armnanotc", "armnanotcplat", "cornanotc2plat", "legnanotcplat",
		"armgplat", "corplat", "armsolar", "armwin", "armmex", "armcom",
	} {
		if isFactory(name) {
			t.Errorf("isFactory(%q) = true, want false", name)
		}
	}
}
