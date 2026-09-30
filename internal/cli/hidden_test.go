package cli

import "testing"

func TestHTTPPort(t *testing.T) {
	cases := []struct {
		name string
		env  string
		dev  bool
		want int
	}{
		{"ephemeral", "", false, 0},
		{"pinned", "7392", false, 7392},
		{"dev wins", "7392", true, devHTTPPort},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(httpPortEnv, c.env)
			got, err := httpPort(c.dev)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("httpPort(%v) = %d, want %d", c.dev, got, c.want)
			}
		})
	}
	t.Setenv(httpPortEnv, "seven")
	if _, err := httpPort(false); err == nil {
		t.Error("httpPort accepted a non-numeric port")
	}
}
