package channelsetup

import "testing"

func TestHasDevChannelFlag(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{name: "claude with flag", argv: []string{"claude", "--dangerously-load-development-channels", "plugin:cc-review@cc-review"}, want: true},
		{name: "flag absent", argv: []string{"claude", "--channels-approved-only"}, want: false},
		{name: "shell", argv: []string{"/bin/fish"}, want: false},
		{name: "empty argv", argv: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDevChannelFlag(tc.argv); got != tc.want {
				t.Errorf("hasDevChannelFlag(%v) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}

func TestLaunchedWithDevChannelsRealOS(t *testing.T) {
	if _, err := LaunchedWithDevChannels(); err != nil {
		t.Fatalf("LaunchedWithDevChannels: %v", err)
	}
}
