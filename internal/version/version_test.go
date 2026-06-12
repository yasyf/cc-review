package version

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"release newer", "v0.6.0", "v0.5.0", true},
		{"release older", "v0.5.0", "v0.6.0", false},
		{"equal is not newer", "v0.5.0", "v0.5.0", false},
		{"patch compares numerically", "v0.10.10", "v0.10.9", true},
		{"major compares numerically", "v1.0.0", "v0.99.99", true},
		{"no v prefix", "0.6.0", "0.5.0", true},
		{"suffix ignored forward", "v0.8.0-1-gabc123", "v0.8.0", false},
		{"suffix ignored backward", "v0.8.0", "v0.8.0-1-gabc123", false},
		{"suffixed but newer triple", "v0.8.1-1-gabc123", "v0.8.0", true},
		{"commit suffix from String", "v0.8.0 (abc123)", "v0.7.0 (def456)", true},
		{"dev beats every release", "dev", "v99.0.0", true},
		{"release never beats dev", "v99.0.0", "dev", false},
		{"dev ties with dev", "dev", "dev", false},
		{"empty behaves as dev", "", "v99.0.0", true},
		{"release never beats empty", "v99.0.0", "", false},
		{"garbage does not beat dev", "garbage", "dev", false},
		{"whitespace trimmed", "  v0.6.0  ", "v0.5.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Newer(tc.a, tc.b); got != tc.want {
				t.Fatalf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
