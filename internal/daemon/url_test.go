package daemon

import "testing"

func TestReviewURL(t *testing.T) {
	if got, want := reviewURL(4123, "abc"), "http://127.0.0.1:4123/s/abc"; got != want {
		t.Errorf("reviewURL = %q, want %q", got, want)
	}
	t.Setenv(urlEnv, "http://127.0.0.1:17001")
	if got, want := reviewURL(4123, "abc"), "http://127.0.0.1:17001/s/abc"; got != want {
		t.Errorf("reviewURL with %s = %q, want %q", urlEnv, got, want)
	}
}
