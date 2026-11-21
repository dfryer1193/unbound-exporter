package service_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/dfryer1193/unbound-exporter/data"
	"github.com/dfryer1193/unbound-exporter/models"
	"github.com/dfryer1193/unbound-exporter/service"
)

type settings struct{ urls []string }

func (settings) GetAdguardUrl() string           { return "https://router" } //nolint:revive,staticcheck // name set by mimir-generate
func (settings) GetAdguardCertSha256() string    { return "" }
func (settings) GetRouterName() string           { return "r" }
func (settings) GetPollInterval() string         { return "10s" }
func (settings) GetRequestTimeout() string       { return "1s" }
func (s settings) GetVictorialogsUrls() []string { return s.urls }
func (settings) GetBufferLimit() int64           { return 100000 }

type source struct {
	rows      []models.Query
	lastSince time.Time
}

func (s *source) QueriesSince(
	_ context.Context,
	_ data.AdGuardSettings,
	since time.Time,
	_ int,
) ([]models.Query, bool, error) {
	s.lastSince = since
	var out []models.Query
	for _, x := range s.rows {
		if x.Time.After(since) {
			out = append(out, x)
		}
	}
	return out, true, nil
}

type store struct {
	down    map[string]bool
	written map[string][]string
	latest  time.Time
}

func (s *store) Write(_ context.Context, base, _ string, qs []models.Query, _ time.Duration) error {
	if s.down[base] {
		return errors.New("down")
	}
	for _, x := range qs {
		s.written[base] = append(s.written[base], x.Domain)
	}
	return nil
}

func (s *store) Latest(context.Context, string, time.Duration, time.Duration) (time.Time, error) {
	return s.latest, nil
}

func newExporter(urls ...string) (*service.Exporter, *source, *store) {
	src := &source{}
	st := &store{down: map[string]bool{}, written: map[string][]string{}}
	e := service.NewExporter(settings{urls}, src, st, slog.New(slog.DiscardHandler))
	e.SetNow(func() time.Time { return at(60) }) // the test log is "recent": within firstStartLookback
	return e, src, st
}

func TestExporterWritesEveryReplicaAndCatchesUpOneThatWasDown(t *testing.T) {
	e, src, st := newExporter("a", "b")
	e.Start(t.Context())

	st.down["b"] = true
	src.rows = newestFirst(q(0, "x"))
	e.Poll(t.Context())
	if !e.Healthy() {
		t.Fatal("one replica reached is healthy")
	}

	st.down["b"] = false
	src.rows = newestFirst(q(0, "x"), q(1, "y"))
	e.Poll(t.Context())

	for _, r := range []string{"a", "b"} {
		if got := st.written[r]; len(got) != 2 || got[0] != "x" || got[1] != "y" {
			t.Fatalf("replica %s got %v, want [x y]", r, got)
		}
	}
}

func TestExporterIsUnhealthyWhenNoReplicaIsReached(t *testing.T) {
	e, src, st := newExporter("a")
	e.Start(t.Context())
	st.down["a"] = true
	src.rows = newestFirst(q(0, "x"))
	e.Poll(t.Context())
	if e.Healthy() {
		t.Fatal("no replica reached: want unhealthy")
	}
}

func TestExporterResumesAfterWhatIsAlreadyWritten(t *testing.T) {
	e, src, st := newExporter("a")
	st.latest = at(5)
	e.Start(t.Context())
	src.rows = newestFirst(q(4, "old"), q(5, "old"), q(6, "new"))
	e.Poll(t.Context())
	if got := st.written["a"]; len(got) != 1 || got[0] != "new" {
		t.Fatalf("got %v, want [new]", got)
	}
}

func TestExporterAsksForEverythingSinceTheLastPollMinusOverlap(t *testing.T) {
	e, src, _ := newExporter("a")
	e.Start(t.Context())
	src.rows = newestFirst(q(0, "x"), q(200, "y"))
	e.Poll(t.Context())
	e.Poll(t.Context())
	if want := at(200).Add(-time.Minute); !src.lastSince.Equal(want) {
		t.Fatalf("second poll asked since %v, want %v", src.lastSince, want)
	}
}
