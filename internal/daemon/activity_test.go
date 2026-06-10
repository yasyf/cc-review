package daemon

import (
	"testing"
	"time"
)

func TestActivityAttachDetachCounts(t *testing.T) {
	a := NewActivity()
	d1 := a.Attach("r1", "channel")
	d2 := a.Attach("r1", "channel")

	if !a.Attached("r1", "channel") {
		t.Fatal("attached after two attaches")
	}
	d1()
	if !a.Attached("r1", "channel") {
		t.Fatal("one open connection must still count as attached")
	}
	d2()
	d2() // double detach must not underflow
	if a.Attached("r1", "channel") {
		t.Fatal("detached after both connections closed")
	}
	if a.Attached("r1", "watch") || a.Attached("r2", "channel") {
		t.Fatal("attachment leaked across consumer or review")
	}
}

func TestActivityPolledSinceWindow(t *testing.T) {
	a := NewActivity()
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }

	a.NotePoll("/repo", "channel")
	if !a.PolledSince("/repo", "channel", 3*time.Second) {
		t.Fatal("fresh poll must count")
	}
	now = now.Add(2 * time.Second)
	if !a.PolledSince("/repo", "channel", 3*time.Second) {
		t.Fatal("poll within the window must count")
	}
	now = now.Add(2 * time.Second)
	if a.PolledSince("/repo", "channel", 3*time.Second) {
		t.Fatal("poll outside the window must not count")
	}
	if a.PolledSince("/repo", "watch", time.Hour) {
		t.Fatal("poll leaked across consumers")
	}
}
