package service

import (
	"slices"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
)

// overlap is how far back from the newest query already seen a poll still
// looks for new ones. AdGuard Home logs a query when its answer is ready, so a
// slow query can appear after faster ones that started later.
const overlap = time.Minute

// cursor picks out the queries in each poll that weren't in earlier polls.
//
// Polls overlap by a minute and log entries have no ids, so the cursor
// remembers how many copies of each identical entry it has already passed on,
// for the entries within overlap of the newest it has seen.
type cursor struct {
	floor  time.Time // entries at or before it were written before a restart
	newest time.Time
	seen   map[models.Query]int
}

// newCursor starts after start, the newest query already written (zero: none).
func newCursor(start time.Time) *cursor {
	return &cursor{floor: start, newest: start, seen: map[models.Query]int{}}
}

// since is the time a poll has to read back to (zero: everything).
func (c *cursor) since() time.Time {
	if c.newest.IsZero() {
		return time.Time{}
	}
	return c.newest.Add(-overlap)
}

// next returns the queries in rows (newest first, as AdGuard Home serves them)
// that weren't returned before, oldest first.
func (c *cursor) next(rows []models.Query) []models.Query {
	cutoff := c.since()
	var fresh []models.Query
	counts := map[models.Query]int{}
	for _, q := range slices.Backward(rows) {
		if !c.newest.IsZero() && (!q.Time.After(cutoff) || !q.Time.After(c.floor)) {
			continue
		}
		counts[q]++
		if counts[q] > c.seen[q] {
			fresh = append(fresh, q)
		}
	}

	for q, n := range counts {
		if n > c.seen[q] {
			c.seen[q] = n
		}
		if q.Time.After(c.newest) {
			c.newest = q.Time
		}
	}
	cutoff = c.since()
	for q := range c.seen {
		if !q.Time.After(cutoff) {
			delete(c.seen, q)
		}
	}
	return fresh
}
