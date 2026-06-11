package daemon

import (
	"testing"
	"time"
)

func TestActivityAttachDetachCounts(t *testing.T) {
	a := NewActivity()
	d1 := a.Attach("r1", "channel", 100)
	d2 := a.Attach("r1", "channel", 100)

	if !a.Attached("r1", "channel", 100) {
		t.Fatal("attached after two attaches")
	}
	d1()
	if !a.Attached("r1", "channel", 100) {
		t.Fatal("one open connection must still count as attached")
	}
	d2()
	d2() // double detach must not underflow
	if a.Attached("r1", "channel", 100) {
		t.Fatal("detached after both connections closed")
	}
	if a.Attached("r1", "watch", 100) || a.Attached("r2", "channel", 100) || a.Attached("r1", "channel", 200) {
		t.Fatal("attachment leaked across consumer, review, or window")
	}
}

func TestActivityPolledSinceWindow(t *testing.T) {
	a := NewActivity()
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }

	a.NotePoll("/repo", "channel", 100)
	if !a.PolledSince("/repo", "channel", 100, 3*time.Second) {
		t.Fatal("fresh poll must count")
	}
	now = now.Add(2 * time.Second)
	if !a.PolledSince("/repo", "channel", 100, 3*time.Second) {
		t.Fatal("poll within the window must count")
	}
	now = now.Add(2 * time.Second)
	if a.PolledSince("/repo", "channel", 100, 3*time.Second) {
		t.Fatal("poll outside the window must not count")
	}
	if a.PolledSince("/repo", "watch", 100, time.Hour) || a.PolledSince("/repo", "channel", 200, time.Hour) {
		t.Fatal("poll leaked across consumers or windows")
	}
}

func TestActivityProven(t *testing.T) {
	a := NewActivity()
	if a.Proven(100) {
		t.Fatal("never-acked window must read unproven")
	}
	a.MarkProven(100)
	if !a.Proven(100) {
		t.Fatal("acked window must read proven")
	}
	if a.Proven(200) {
		t.Fatal("proof leaked across windows")
	}
	detach := a.Attach("r1", "channel", 100)
	detach()
	if !a.Proven(100) {
		t.Fatal("proof is daemon-lifetime and must survive an SSE detach")
	}
}

func TestActivityAttachedWithinGrace(t *testing.T) {
	a := NewActivity()
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }

	if a.AttachedWithin("r1", 10*time.Second) {
		t.Fatal("never-attached review must read unattached")
	}
	dChannel := a.Attach("r1", "channel", 100)
	dWatch := a.Attach("r1", "watch", 200)
	if !a.AttachedWithin("r1", 10*time.Second) {
		t.Fatal("a live attachment of any consumer must count")
	}

	dChannel()
	now = now.Add(time.Hour)
	if !a.AttachedWithin("r1", 10*time.Second) {
		t.Fatal("the surviving attachment must keep the review occupied")
	}

	dWatch()
	now = now.Add(5 * time.Second)
	if !a.AttachedWithin("r1", 10*time.Second) {
		t.Fatal("a drop within the grace must still read occupied")
	}
	now = now.Add(6 * time.Second)
	if a.AttachedWithin("r1", 10*time.Second) {
		t.Fatal("a drop past the grace must read unattached")
	}
	if a.AttachedWithin("r2", time.Hour) {
		t.Fatal("grace leaked across reviews")
	}
}
