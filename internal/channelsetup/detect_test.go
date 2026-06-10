package channelsetup

import "testing"

func TestAncestryContains(t *testing.T) {
	// 1 (init) -> 42 (claude --dangerously...) -> 99 (shell) -> 123 (cc-review)
	table := map[int]procEntry{
		123: {ppid: 99, cmd: "cc-review setup-channels --check"},
		99:  {ppid: 42, cmd: "/bin/fish"},
		42:  {ppid: 1, cmd: "claude --dangerously-load-development-channels plugin:review@cc-review"},
		1:   {ppid: 0, cmd: "/sbin/launchd"},
	}
	cases := []struct {
		name   string
		start  int
		needle string
		want   bool
	}{
		{name: "flag on grandparent claude", start: 123, needle: devChannelFlag, want: true},
		{name: "flag absent in chain", start: 123, needle: "--channels-approved-only", want: false},
		{name: "start not in table", start: 555, needle: devChannelFlag, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ancestryContains(table, tc.start, tc.needle); got != tc.want {
				t.Errorf("ancestryContains = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAncestryContainsBreaksCycle(t *testing.T) {
	// A malformed table with a ppid cycle must not loop forever.
	table := map[int]procEntry{
		10: {ppid: 20, cmd: "a"},
		20: {ppid: 10, cmd: "b"},
	}
	if ancestryContains(table, 10, devChannelFlag) {
		t.Error("expected false for a cyclic table with no match")
	}
}
