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
func (c Cursor) Next(rows []models.Query) []models.Query { return c.c.next(rows) }

// Since exposes cursor.since.
func (c Cursor) Since() time.Time { return c.c.since() }

// Remembered is how many distinct rows the cursor still remembers.
func (c Cursor) Remembered() int { return len(c.c.seen) }

// Start exposes the start of Run: finding where to resume.
func (e *Exporter) Start(ctx context.Context) { e.cursor = newCursor(e.resumePoint(ctx)) }

// Poll exposes one poll of Run.
func (e *Exporter) Poll(ctx context.Context) { e.poll(ctx) }

// SetNow replaces the Exporter's clock.
func (e *Exporter) SetNow(now func() time.Time) { e.now = now }
