package ui

import "fmt"

// GradientStops devolve n cores interpoladas linearmente em RGB entre start e
// end (inclusive), para pintar assinaturas de marca em blocos de linha
// (ex.: banner ASCII da TUI). Com n <= 1 devolve apenas [start]. Cores fora do
// formato "#rrggbb" degradam para réplicas de start — os tokens são
// constantes validadas, então o fallback é defensivo.
func GradientStops(start, end string, n int) []string {
	if n <= 1 {
		return []string{start}
	}
	r0, g0, b0, ok0 := parseHex(start)
	r1, g1, b1, ok1 := parseHex(end)
	if !ok0 || !ok1 {
		stops := make([]string, n)
		for i := range stops {
			stops[i] = start
		}
		return stops
	}
	stops := make([]string, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n-1)
		r := r0 + int32(t*float64(r1-r0))
		g := g0 + int32(t*float64(g1-g0))
		b := b0 + int32(t*float64(b1-b0))
		stops[i] = fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	return stops
}

func parseHex(c string) (r, g, b int32, ok bool) {
	if len(c) != 7 || c[0] != '#' {
		return 0, 0, 0, false
	}
	_, err := fmt.Sscanf(c, "#%02x%02x%02x", &r, &g, &b)
	return r, g, b, err == nil
}
