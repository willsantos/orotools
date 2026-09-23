package ui

import "testing"

func TestGradientStopsEndpointsAndLength(t *testing.T) {
	stops := GradientStops(ColorGradientStart, ColorGradientEnd, 6)
	if len(stops) != 6 {
		t.Fatalf("len = %d, want 6", len(stops))
	}
	if stops[0] != ColorGradientStart {
		t.Errorf("primeiro stop = %s, want %s", stops[0], ColorGradientStart)
	}
	if stops[5] != ColorGradientEnd {
		t.Errorf("último stop = %s, want %s", stops[5], ColorGradientEnd)
	}
	for i, s := range stops {
		if len(s) != 7 || s[0] != '#' {
			t.Errorf("stop %d = %q, fora do formato #rrggbb", i, s)
		}
	}
}

func TestGradientStopsDegenerateCases(t *testing.T) {
	if got := GradientStops("#112233", "#445566", 1); len(got) != 1 || got[0] != "#112233" {
		t.Errorf("n=1 deveria devolver [start], got %v", got)
	}
	if got := GradientStops("#112233", "#445566", 0); len(got) != 1 || got[0] != "#112233" {
		t.Errorf("n=0 deveria devolver [start], got %v", got)
	}
	two := GradientStops("#000000", "#ffffff", 2)
	if two[0] != "#000000" || two[1] != "#ffffff" {
		t.Errorf("n=2 deveria devolver os extremos, got %v", two)
	}
}

func TestGradientStopsInvalidColorFallsBack(t *testing.T) {
	stops := GradientStops("nope", ColorGradientEnd, 3)
	for i, s := range stops {
		if s != "nope" {
			t.Errorf("stop %d = %q, want fallback \"nope\"", i, s)
		}
	}
}
