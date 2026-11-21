package data_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/dfryer1193/unbound-exporter/data"
	"github.com/dfryer1193/unbound-exporter/models"
)

// entry is a query log entry as AdGuard Home serves it.
func entry(sec int, name, reason string) map[string]any {
	e := map[string]any{
		"time":          time.Date(2026, 11, 21, 12, 0, sec, 0, time.UTC).Format(time.RFC3339Nano),
		"client":        "192.168.86.49",
		"client_info":   map[string]any{"name": "pihole-5"},
		"question":      map[string]any{"name": name, "type": "A"},
		"reason":        reason,
		"status":        "NOERROR",
		"elapsedMs":     "1.5",
		"cached":        false,
		"upstream":      "127.0.0.1:53530",
		"answer_dnssec": true,
		"rules":         []any{},
		"answer":        []any{map[string]any{"ttl": 300}},
	}
	if reason == "FilteredBlackList" {
		e["rules"] = []any{map[string]any{"filter_list_id": 1, "text": "0.0.0.0 " + name}}
	}
	return e
}

// fakeAdGuard serves log newest first, two entries a page, paging by
// older_than like AdGuard Home.
func fakeAdGuard(t *testing.T, log []map[string]any) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/control/filtering/status":
			_ = json.NewEncoder(w).
				Encode(map[string]any{"filters": []any{map[string]any{"id": 1, "name": "Steven Black"}}})
		case "/control/querylog":
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			start := 0
			if ot := r.URL.Query().Get("older_than"); ot != "" {
				for start < len(log) && log[start]["time"].(string) >= ot {
					start++
				}
			}
			page := log[start:min(len(log), start+min(limit, 2))]
			oldest := ""
			if len(page) > 0 {
				oldest = page[len(page)-1]["time"].(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": page, "oldest": oldest})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	return srv, hex.EncodeToString(sum[:])
}

func TestQueriesSincePagesBackToSinceAndMapsFields(t *testing.T) {
	srv, pin := fakeAdGuard(t, []map[string]any{
		entry(5, "e.com", "NotFilteredNotFound"),
		entry(4, "doubleclick.net", "FilteredBlackList"),
		entry(3, "c.com", "NotFilteredNotFound"),
		entry(2, "b.com", "NotFilteredNotFound"),
		entry(1, "a.com", "NotFilteredNotFound"),
	})
	a := data.NewAdGuard("u", "p")
	s := data.AdGuardSettings{URL: srv.URL, CertSHA256: pin, Timeout: 5 * time.Second}

	qs, complete, err := a.QueriesSince(t.Context(), s, time.Date(2026, 11, 21, 12, 0, 2, 0, time.UTC), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !complete || len(qs) != 3 || qs[0].Domain != "e.com" || qs[2].Domain != "c.com" {
		t.Fatalf("got complete=%v %d queries %+v, want e, doubleclick, c", complete, len(qs), qs)
	}
	want := models.Query{
		Time:          time.Date(2026, 11, 21, 12, 0, 4, 0, time.UTC),
		Client:        "pihole-5",
		ClientIP:      "192.168.86.49",
		Family:        "ip4",
		Type:          "A",
		Domain:        "doubleclick.net",
		Action:        "Block",
		Source:        "Blocklist",
		Blocklist:     "Steven Black",
		Rule:          "0.0.0.0 doubleclick.net",
		Reason:        "FilteredBlackList",
		RCode:         "NOERROR",
		DNSSEC:        "Secure",
		ResolveTimeMS: 1.5,
		TTL:           300,
	}
	if qs[1] != want {
		t.Fatalf("blocked query mapped wrong:\n got %+v\nwant %+v", qs[1], want)
	}
	if qs[0].Blocked() || qs[0].Source != "Upstream" {
		t.Fatalf("passed query mapped wrong: %+v", qs[0])
	}
}

func TestQueriesSinceStopsAtMaxRows(t *testing.T) {
	srv, pin := fakeAdGuard(t, []map[string]any{
		entry(4, "d.com", "NotFilteredNotFound"),
		entry(3, "c.com", "NotFilteredNotFound"),
		entry(2, "b.com", "NotFilteredNotFound"),
		entry(1, "a.com", "NotFilteredNotFound"),
	})
	qs, complete, err := data.NewAdGuard("u", "p").QueriesSince(
		t.Context(), data.AdGuardSettings{URL: srv.URL, CertSHA256: pin, Timeout: 5 * time.Second}, time.Time{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if complete || len(qs) != 2 {
		t.Fatalf("got complete=%v with %d queries, want incomplete with 2", complete, len(qs))
	}
}

func TestQueriesSinceRejectsTheWrongCertificate(t *testing.T) {
	srv, _ := fakeAdGuard(t, nil)
	_, _, err := data.NewAdGuard("u", "p").QueriesSince(t.Context(), data.AdGuardSettings{
		URL: srv.URL, CertSHA256: hex.EncodeToString(make([]byte, sha256.Size)), Timeout: 5 * time.Second,
	}, time.Time{}, 10)
	if err == nil {
		t.Fatal("want an error for a certificate that doesn't match the pin")
	}
}
