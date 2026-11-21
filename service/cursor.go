package service

import (
	"slices"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
)

// overlap is how far back from the newest query already seen a poll still
// looks for new ones. OPNsense writes its query log in batches, so a row can
// appear a little after rows newer than it.
const overlap = time.Minute

// cursor picks out the queries in each poll that weren't in earlier polls.
//
// OPNsense's query log has no ids and only second resolution, and the same
// query can legitimately happen several times in one second. So the cursor
// remembers how many copies of each identical row it has already passed on,
// for the rows within overlap of the newest it has seen.
type cursor struct {
	floor  time.Time // rows at or before it were written before a restart
	newest time.Time
	seen   map[models.Query]int
}

// newCursor starts after start, the newest query already written (zero: none).
func newCursor(start time.Time) *cursor {
	return &cursor{floor: start, newest: start, seen: map[models.Query]int{}}
}

// next returns the queries in rows (newest first, as OPNsense serves them)
// that weren't returned before, oldest first. gap reports that queries may
// have been lost: the poll's oldest row is newer than everything seen, so
// OPNsense may have dropped rows in between.
func (c *cursor) next(rows []models.Query, limit int) ([]models.Query, bool) {
	if len(rows) == 0 {
		return nil, false
	}

	cutoff := c.newest.Add(-overlap)
	oldest := rows[len(rows)-1].Time
	gap := !c.newest.IsZero() && len(rows) >= limit && oldest.After(c.newest)

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
	cutoff = c.newest.Add(-overlap)
	for q := range c.seen {
		if !q.Time.After(cutoff) {
			delete(c.seen, q)
		}
	}
	return fresh, gap
}
