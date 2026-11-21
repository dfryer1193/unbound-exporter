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

func (settings) GetOpnsenseUrl() string          { return "https://router" } //nolint:revive,staticcheck // name set by mimir-generate
func (settings) GetOpnsenseCertSha256() string   { return "" }
func (settings) GetRouterName() string           { return "r" }
func (settings) GetPollInterval() string         { return "10s" }
func (settings) GetRequestTimeout() string       { return "1s" }
func (s settings) GetVictorialogsUrls() []string { return s.urls }
func (settings) GetBufferLimit() int64           { return 100000 }

type source struct{ rows []models.Query }

func (s *source) RecentQueries(context.Context, data.OPNsenseSettings) ([]models.Query, error) {
	return s.rows, nil
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
