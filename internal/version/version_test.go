package version

import "testing"

func TestTagAndString(t *testing.T) {
	cases := []struct {
		name       string
		version    string
		commit     string
		wantTag    string
		wantString string
	}{
		{"release build", "v0.19.2", "abc1234", "v0.19.2", "v0.19.2 (abc1234)"},
		{"release build no commit", "v0.19.2", "", "v0.19.2", "v0.19.2"},
		{"dev build", "dev", "abc1234", "dev", "dev (abc1234)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func(v, c string) { Version, Commit = v, c }(Version, Commit)
			Version, Commit = tc.version, tc.commit
			if got := Tag(); got != tc.wantTag {
				t.Errorf("Tag() = %q, want %q", got, tc.wantTag)
			}
			if got := String(); got != tc.wantString {
				t.Errorf("String() = %q, want %q", got, tc.wantString)
			}
		})
	}
}

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
