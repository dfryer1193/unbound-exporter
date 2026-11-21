package service_test

import (
	"testing"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
	"github.com/dfryer1193/unbound-exporter/service"
)

// at returns a time sec seconds into the test day.
func at(sec int) time.Time {
	return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC).Add(time.Duration(sec) * time.Second)
}

func q(sec int, domain string) models.Query {
	return models.Query{Time: at(sec), Domain: domain}
}

// newestFirst returns qs reversed, the order OPNsense serves rows in.
func newestFirst(qs ...models.Query) []models.Query {
	out := make([]models.Query, len(qs))
	for i, x := range qs {
		out[len(qs)-1-i] = x
	}
	return out
}

func domains(qs []models.Query) []string {
	out := make([]string, len(qs))
	for i, x := range qs {
		out[i] = x.Domain
	}
	return out
}

func equal(t *testing.T, got []models.Query, want ...string) {
	t.Helper()
	g := domains(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("got %v, want %v", g, want)
		}
	}
}

func TestCursorFirstPollReturnsEverythingOldestFirst(t *testing.T) {
	c := service.NewCursor(time.Time{})
	fresh := c.Next(newestFirst(q(0, "a"), q(1, "b"), q(2, "c")))
	equal(t, fresh, "a", "b", "c")
}

func TestCursorSkipsRowsAlreadyReturned(t *testing.T) {
	c := service.NewCursor(time.Time{})
	c.Next(newestFirst(q(0, "a"), q(1, "b")))
	fresh := c.Next(newestFirst(q(0, "a"), q(1, "b"), q(2, "c")))
	equal(t, fresh, "c")
}

func TestCursorKeepsRepeatsWithinOneSecond(t *testing.T) {
	c := service.NewCursor(time.Time{})
	c.Next(newestFirst(q(5, "x")))
	// The same query twice more in the same second: one is the row already
	// returned, the other is new.
	fresh := c.Next(newestFirst(q(5, "x"), q(5, "x")))
	equal(t, fresh, "x")
	fresh = c.Next(newestFirst(q(5, "x"), q(5, "x")))
	equal(t, fresh)
}

func TestCursorPicksUpLateRowsWithinOverlap(t *testing.T) {
	c := service.NewCursor(time.Time{})
	c.Next(newestFirst(q(0, "a"), q(30, "b")))
	// A row 20s older than the newest seen shows up late.
	fresh := c.Next(newestFirst(q(0, "a"), q(10, "late"), q(30, "b")))
	equal(t, fresh, "late")
}

func TestCursorIgnoresRowsOlderThanOverlap(t *testing.T) {
	c := service.NewCursor(time.Time{})
	c.Next(newestFirst(q(100, "a")))
	fresh := c.Next(newestFirst(q(10, "ancient"), q(100, "a"), q(101, "b")))
	equal(t, fresh, "b")
}

func TestCursorStartingPointSkipsOlderRows(t *testing.T) {
	// Resuming after a restart from what VictoriaLogs already holds.
	c := service.NewCursor(at(100))
	fresh := c.Next(newestFirst(q(10, "old"), q(90, "written"), q(100, "written"), q(101, "new")))
	equal(t, fresh, "new")
}

func TestCursorSinceReachesBackOneOverlapBeforeTheNewest(t *testing.T) {
	c := service.NewCursor(time.Time{})
	if !c.Since().IsZero() {
		t.Fatal("a fresh cursor reads everything")
	}
	c.Next(newestFirst(q(0, "a"), q(100, "b")))
	if want := at(100).Add(-time.Minute); !c.Since().Equal(want) {
		t.Fatalf("since = %v, want %v", c.Since(), want)
	}
}

func TestCursorForgetsRowsOutsideOverlap(t *testing.T) {
	c := service.NewCursor(time.Time{})
	c.Next(newestFirst(q(0, "a")))
	c.Next(newestFirst(q(0, "a"), q(1000, "b")))
	if n := c.Remembered(); n != 1 {
		t.Fatalf("remembers %d rows, want 1", n)
	}
}
