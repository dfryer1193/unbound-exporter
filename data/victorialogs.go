package data

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
)

// Stream is the VictoriaLogs stream every query is written to. Queries select
// it with {source="opnsense-unbound"}.
const (
	StreamField = "source"
	StreamValue = "opnsense-unbound"
)

// errorBodyLimit is how much of an error response to quote.
const errorBodyLimit = 512

// VictoriaLogs writes queries to one VictoriaLogs server and reads back how
// far it has got.
type VictoriaLogs struct {
	client *http.Client
}

// NewVictoriaLogs returns a VictoriaLogs client.
func NewVictoriaLogs() *VictoriaLogs {
	return &VictoriaLogs{client: &http.Client{}}
}

// logLine is one query as a VictoriaLogs JSON line. Every field is
// searchable; _msg is the line shown in log views.
type logLine struct {
	Time          string  `json:"_time"`
	Msg           string  `json:"_msg"`
	Source        string  `json:"source"`
	Router        string  `json:"router"`
	Client        string  `json:"client"`
	ClientIP      string  `json:"client_ip"`
	Family        string  `json:"family"`
	Type          string  `json:"type"`
	Domain        string  `json:"domain"`
	Action        string  `json:"action"`
	Blocked       bool    `json:"blocked"`
	AnswerSource  string  `json:"answer_source"`
	Blocklist     string  `json:"blocklist,omitempty"`
	RCode         string  `json:"rcode"`
	DNSSEC        string  `json:"dnssec"`
	Rule          string  `json:"rule,omitempty"`
	Reason        string  `json:"reason"`
	ResolveTimeMS float64 `json:"resolve_time_ms"`
	TTL           int64   `json:"ttl"`
}

// Write sends queries to the server at base (e.g. http://host:9428).
func (v *VictoriaLogs) Write(
	ctx context.Context, base, router string, queries []models.Query, timeout time.Duration,
) error {
	if len(queries) == 0 {
		return nil
	}

	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, q := range queries {
		domain := strings.TrimSuffix(q.Domain, ".")
		line := logLine{
			Time:          q.Time.UTC().Format(time.RFC3339Nano),
			Msg:           fmt.Sprintf("%s %s %s %s %s", q.Client, q.Type, domain, q.Action, q.RCode),
			Source:        StreamValue,
			Router:        router,
			Client:        q.Client,
			ClientIP:      q.ClientIP,
			Family:        q.Family,
			Type:          q.Type,
			Domain:        domain,
			Action:        q.Action,
			Blocked:       q.Blocked(),
			AnswerSource:  q.Source,
			Blocklist:     q.Blocklist,
			RCode:         q.RCode,
			DNSSEC:        q.DNSSEC,
			Rule:          q.Rule,
			Reason:        q.Reason,
			ResolveTimeMS: q.ResolveTimeMS,
			TTL:           q.TTL,
		}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}

	params := url.Values{"_stream_fields": {StreamField + ",router"}, "_time_field": {"_time"}, "_msg_field": {"_msg"}}
	resp, err := v.do(ctx, http.MethodPost, base+"/insert/jsonline?"+params.Encode(), &body, timeout)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return fmt.Errorf("%s: HTTP %d: %s", base, resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}

// Latest returns the time of the newest query the server at base holds from
// the last lookback, or the zero time if it holds none.
func (v *VictoriaLogs) Latest(ctx context.Context, base string, lookback, timeout time.Duration) (time.Time, error) {
	q := fmt.Sprintf(`{%s=%q} _time:%s | stats max(_time) as latest`, StreamField, StreamValue, lookback)
	resp, err := v.do(
		ctx,
		http.MethodGet,
		base+"/select/logsql/query?"+url.Values{"query": {q}}.Encode(),
		http.NoBody,
		timeout,
	)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("%s: HTTP %d", base, resp.StatusCode)
	}

	var latest time.Time
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var row struct {
			Latest string `json:"latest"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil || row.Latest == "" {
			continue
		}
		if t, perr := time.Parse(time.RFC3339Nano, row.Latest); perr == nil && t.After(latest) {
			latest = t
		}
	}
	return latest, sc.Err()
}

func (v *VictoriaLogs) do(
	ctx context.Context,
	method, u string,
	body io.Reader,
	timeout time.Duration,
) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelOnClose{resp.Body, cancel}
	return resp, nil
}

// cancelOnClose releases a request's context once its body is closed.
type cancelOnClose struct {
	io.ReadCloser

	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
