package video

import "testing"

// TestStartRefFollowsTheGrammar reads the start expressions HyperFrames
// accepts, including ids that contain a dash and offsets without a space.
func TestStartRefFollowsTheGrammar(t *testing.T) {
	for _, tc := range []struct {
		expr string
		id   string
		sign int
		off  float64
		ref  bool
	}{
		{"0", "", 1, 0, false},
		{"2.5", "", 1, 0, false},
		{"-1", "", 1, 0, false},
		{"intro", "intro", 1, 0, true},
		{"scene-01", "scene-01", 1, 0, true},
		{"intro + 0.5", "intro", 1, 0.5, true},
		{"intro +.5", "intro", 1, 0.5, true},
		{"intro-0.5", "intro-0.5", 1, 0, true}, // a dash and a dot are part of an id
		{"intro - 2", "intro", -1, 2, true},
		{"intro + n", "", 1, 0, false},
		{"", "", 1, 0, false},
	} {
		id, sign, off, ref := startRef(tc.expr)
		if ref != tc.ref || id != tc.id || sign != tc.sign || off != tc.off {
			t.Errorf("startRef(%q) = %q %d %v %v, want %q %d %v %v", tc.expr, id, sign, off, ref, tc.id, tc.sign, tc.off, tc.ref)
		}
	}
}

// TestStartsResolveThroughChains resolves a clip that starts when another
// ends, through more than one step.
func TestStartsResolveThroughChains(t *testing.T) {
	clips := []*Clip{
		{ID: "a", StartExpr: "0", HasStart: true, Duration: 2, HasDuration: true},
		{ID: "b", StartExpr: "a", HasStart: true, Duration: 3, HasDuration: true},
		{ID: "c", StartExpr: "b - 1", HasStart: true, Duration: 1, HasDuration: true},
		{ID: "loop", StartExpr: "loop", HasStart: true, Duration: 1, HasDuration: true},
	}
	resolveStarts(clips)
	if clips[1].Start != 2 || !clips[1].resolved {
		t.Fatalf("b = %+v", clips[1])
	}
	if clips[2].Start != 4 || !clips[2].resolved {
		t.Fatalf("c = %+v", clips[2])
	}
	if clips[3].resolved || !unresolvedStart(clips[3]) {
		t.Fatalf("a cycle resolved: %+v", clips[3])
	}
}

// TestNegativeStartClamps keeps a clip from starting before the composition.
func TestNegativeStartClamps(t *testing.T) {
	clips := []*Clip{{ID: "a", StartExpr: "-2", HasStart: true, Duration: 2, HasDuration: true}}
	resolveStarts(clips)
	if clips[0].Start != 0 {
		t.Fatalf("start = %v", clips[0].Start)
	}
}
