package service

import (
	"context"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
)

// Cursor exposes cursor to the external test package.
type Cursor struct{ c *cursor }

// NewCursor exposes newCursor.
func NewCursor(start time.Time) Cursor { return Cursor{newCursor(start)} }

// Next exposes cursor.next.
func (c Cursor) Next(rows []models.Query, limit int) ([]models.Query, bool) {
	return c.c.next(rows, limit)
}

// Remembered is how many distinct rows the cursor still remembers.
func (c Cursor) Remembered() int { return len(c.c.seen) }

// Start exposes the start of Run: finding where to resume.
func (e *Exporter) Start(ctx context.Context) { e.cursor = newCursor(e.resumePoint(ctx)) }

// Poll exposes one poll of Run.
func (e *Exporter) Poll(ctx context.Context) { e.poll(ctx) }
