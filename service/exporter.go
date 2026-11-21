package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/dfryer1193/unbound-exporter/data"
	"github.com/dfryer1193/unbound-exporter/models"
)

// maxRowsPerPoll caps how far back one poll pages through AdGuard Home's log,
// so a long outage is caught up over several polls rather than in one huge one.
// Anything older than what a capped poll reaches is skipped (and logged).
const maxRowsPerPoll = 50000

// firstStartLookback is how much history to copy when VictoriaLogs holds none.
const firstStartLookback = 10 * time.Minute

// resumeLookback is how far back to look in VictoriaLogs for the newest query
// already written, when starting.
const resumeLookback = 24 * time.Hour

const (
	defaultPollInterval   = 10 * time.Second
	defaultRequestTimeout = 15 * time.Second
	minPollInterval       = time.Second

	// healthyPolls is how many poll intervals may pass without a successful
	// poll before the Exporter reports unhealthy.
	healthyPolls = 3
)

// Settings is what the Exporter reads on every poll, so changes apply live.
// ExporterConfig (generated from exporter_config.json, kept current by Mimir)
// satisfies it.
type Settings interface {
	GetAdguardUrl() string
	GetAdguardCertSha256() string
	GetRouterName() string
	GetPollInterval() string
	GetRequestTimeout() string
	GetVictorialogsUrls() []string
	GetBufferLimit() int64
}

// QuerySource is the router's query log.
type QuerySource interface {
	// QueriesSince returns the queries logged after since, newest first, at
	// most maxRows; complete is false when it stopped before reaching since.
	QueriesSince(
		ctx context.Context,
		s data.AdGuardSettings,
		since time.Time,
		maxRows int,
	) ([]models.Query, bool, error)
}

// LogStore is one kind of VictoriaLogs server.
type LogStore interface {
	Write(ctx context.Context, base, router string, queries []models.Query, timeout time.Duration) error
	Latest(ctx context.Context, base string, lookback, timeout time.Duration) (time.Time, error)
}

// Exporter copies the router's DNS query log into VictoriaLogs.
type Exporter struct {
	settings Settings
	source   QuerySource
	store    LogStore
	logger   *slog.Logger
	now      func() time.Time

	cursor  *cursor
	pending map[string][]models.Query // per VictoriaLogs URL, oldest first

	mu       sync.Mutex
	lastPoll time.Time // last poll that read the router and reached at least one replica
}

// NewExporter returns an Exporter. Call Run to start it.
func NewExporter(settings Settings, source QuerySource, store LogStore, logger *slog.Logger) *Exporter {
	return &Exporter{
		settings: settings, source: source, store: store, logger: logger, now: time.Now,
		pending: map[string][]models.Query{},
	}
}

// Run polls until ctx is done.
func (e *Exporter) Run(ctx context.Context) {
	e.cursor = newCursor(e.resumePoint(ctx))
	for {
		e.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.pollInterval()):
		}
	}
}

// Healthy reports whether a poll succeeded recently: within three poll
// intervals, and never less than a minute.
func (e *Exporter) Healthy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	window := max(healthyPolls*e.pollInterval(), time.Minute)
	return !e.lastPoll.IsZero() && e.now().Sub(e.lastPoll) < window
}

// resumePoint is the newest query any replica already holds, so a restart
// neither repeats nor skips queries.
func (e *Exporter) resumePoint(ctx context.Context) time.Time {
	var newest time.Time
	for _, u := range e.settings.GetVictorialogsUrls() {
		t, err := e.store.Latest(ctx, u, resumeLookback, e.requestTimeout())
		if err != nil {
			e.logger.WarnContext(ctx, "Couldn't read where VictoriaLogs got to", "url", u, "error", err)
			continue
		}
		if t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		e.logger.InfoContext(
			ctx,
			"Nothing written yet; starting from the last few minutes",
			"lookback",
			firstStartLookback,
		)
	} else {
		e.logger.InfoContext(ctx, "Resuming after the newest query already written", "time", newest)
	}
	return newest
}

func (e *Exporter) poll(ctx context.Context) {
	since := e.cursor.since()
	if since.IsZero() {
		since = e.now().Add(-firstStartLookback)
	}
	rows, complete, err := e.source.QueriesSince(ctx, data.AdGuardSettings{
		URL:        e.settings.GetAdguardUrl(),
		CertSHA256: e.settings.GetAdguardCertSha256(),
		Timeout:    e.requestTimeout(),
	}, since, maxRowsPerPoll)
	if err != nil {
		e.logger.ErrorContext(ctx, "Couldn't read AdGuard Home's query log", "error", err)
		return
	}
	if !complete && len(rows) > 0 {
		e.logger.WarnContext(ctx, "Skipping queries: more were logged since the last poll than one poll reads",
			"skipped_before", rows[len(rows)-1].Time, "since", since)
	}

	fresh := e.cursor.next(rows)

	urls := e.settings.GetVictorialogsUrls()
	limit := max(int(e.settings.GetBufferLimit()), maxRowsPerPoll)
	reached := 0
	var errs []error
	for _, u := range urls {
		batch := slices.Concat(e.pending[u], fresh)
		if dropped := len(batch) - limit; dropped > 0 {
			e.logger.WarnContext(
				ctx,
				"Dropping buffered queries for an unreachable replica",
				"url",
				u,
				"dropped",
				dropped,
			)
			batch = batch[dropped:]
		}
		if err = e.store.Write(ctx, u, e.settings.GetRouterName(), batch, e.requestTimeout()); err != nil {
			e.pending[u] = batch
			errs = append(errs, err)
			continue
		}
		delete(e.pending, u)
		reached++
	}
	e.forgetRemovedReplicas(urls)

	if err = errors.Join(errs...); err != nil {
		e.logger.ErrorContext(ctx, "Couldn't write to VictoriaLogs; buffered for the next poll", "error", err)
	}
	if reached > 0 {
		e.mu.Lock()
		e.lastPoll = e.now()
		e.mu.Unlock()
	}
	e.logger.DebugContext(ctx, "Polled", "rows", len(rows), "new", len(fresh), "replicas_reached", reached)
}

// forgetRemovedReplicas drops buffers for URLs no longer configured.
func (e *Exporter) forgetRemovedReplicas(urls []string) {
	keep := make(map[string]bool, len(urls))
	for _, u := range urls {
		keep[u] = true
	}
	for u := range e.pending {
		if !keep[u] {
			delete(e.pending, u)
		}
	}
}

func (e *Exporter) pollInterval() time.Duration {
	return e.duration("poll_interval", e.settings.GetPollInterval(), defaultPollInterval)
}

func (e *Exporter) requestTimeout() time.Duration {
	return e.duration("request_timeout", e.settings.GetRequestTimeout(), defaultRequestTimeout)
}

// duration parses a Go duration setting, falling back to def (and saying so)
// if it isn't one.
func (e *Exporter) duration(name, value string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil || d < minPollInterval {
		e.logger.Warn("Ignoring an unusable duration setting", "item", name, "value", value, "using", def)
		return def
	}
	return d
}
