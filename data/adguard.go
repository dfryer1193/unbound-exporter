package data

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
)

// pageSize is how many query log entries are asked for per request.
const pageSize = 1000

// customRulesID is the filter_list_id AdGuard Home gives its own user rules.
const customRulesID = 0

// AdGuard reads AdGuard Home's query log through its API.
type AdGuard struct {
	user, password string
	client         *http.Client
}

// AdGuardSettings is where and how to reach AdGuard Home.
type AdGuardSettings struct {
	URL        string        // e.g. https://192.168.90.1:3443
	CertSHA256 string        // hex sha256 of AdGuard Home's (self-signed) certificate, DER
	Timeout    time.Duration // per request
}

// NewAdGuard returns a reader that logs in as user.
func NewAdGuard(user, password string) *AdGuard {
	return &AdGuard{user: user, password: password, client: &http.Client{}}
}

// logEntry is one entry of GET /control/querylog.
type logEntry struct {
	Time       time.Time `json:"time"`
	Client     string    `json:"client"`
	ClientInfo struct {
		Name string `json:"name"`
	} `json:"client_info"`
	Question struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"question"`
	Reason    string `json:"reason"`
	Status    string `json:"status"`
	ElapsedMS string `json:"elapsedMs"`
	Cached    bool   `json:"cached"`
	Upstream  string `json:"upstream"`
	DNSSEC    bool   `json:"answer_dnssec"`
	Rules     []struct {
		FilterListID int64  `json:"filter_list_id"`
		Text         string `json:"text"`
	} `json:"rules"`
	Answer []struct {
		TTL int64 `json:"ttl"`
	} `json:"answer"`
}

// QueriesSince returns the queries AdGuard Home logged after since, newest
// first, paging back through its log. It stops after maxRows; complete reports
// whether it got back to since (false: there were more, and they're missed).
func (a *AdGuard) QueriesSince(
	ctx context.Context, s AdGuardSettings, since time.Time, maxRows int,
) ([]models.Query, bool, error) {
	transport, err := pinnedTransport(s.CertSHA256)
	if err != nil {
		return nil, false, err
	}
	defer transport.CloseIdleConnections()
	client := *a.client
	client.Transport = transport
	base := strings.TrimSuffix(s.URL, "/") + "/control/"

	lists, err := a.filterNames(ctx, &client, base, s.Timeout)
	if err != nil {
		return nil, false, err
	}

	var queries []models.Query
	olderThan := ""
	for len(queries) < maxRows {
		params := url.Values{"limit": {strconv.Itoa(pageSize)}}
		if olderThan != "" {
			params.Set("older_than", olderThan)
		}
		var page struct {
			Data   []logEntry `json:"data"`
			Oldest string     `json:"oldest"`
		}
		if err = a.get(ctx, &client, base+"querylog?"+params.Encode(), s.Timeout, &page); err != nil {
			return nil, false, err
		}
		for _, e := range page.Data {
			if !e.Time.After(since) {
				return queries, true, nil
			}
			queries = append(queries, toQuery(e, lists))
		}
		if len(page.Data) == 0 || page.Oldest == "" || page.Oldest == olderThan {
			return queries, true, nil // the start of the log
		}
		olderThan = page.Oldest
	}
	return queries, false, nil
}

// filterNames maps filter list ids to names, for reporting which list blocked.
func (a *AdGuard) filterNames(
	ctx context.Context,
	c *http.Client,
	base string,
	timeout time.Duration,
) (map[int64]string, error) {
	var status struct {
		Filters []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"filters"`
		WhitelistFilters []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"whitelist_filters"`
	}
	if err := a.get(ctx, c, base+"filtering/status", timeout, &status); err != nil {
		return nil, err
	}
	names := map[int64]string{customRulesID: "Custom rules"}
	for _, f := range append(status.Filters, status.WhitelistFilters...) {
		names[f.ID] = f.Name
	}
	return names, nil
}

func (a *AdGuard) get(ctx context.Context, c *http.Client, u string, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.user, a.password)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("adguard: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("adguard %s: HTTP %d", req.URL.Path, resp.StatusCode)
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("adguard %s: %w", req.URL.Path, err)
	}
	return nil
}

func toQuery(e logEntry, lists map[int64]string) models.Query {
	q := models.Query{
		Time:     e.Time.UTC(),
		Client:   e.Client,
		ClientIP: e.Client,
		Family:   "ip4",
		Type:     e.Question.Type,
		Domain:   strings.TrimSuffix(e.Question.Name, "."),
		Action:   "Pass",
		Reason:   e.Reason,
		RCode:    e.Status,
		DNSSEC:   "Insecure",
	}
	if e.ClientInfo.Name != "" {
		q.Client = e.ClientInfo.Name
	}
	if ip := net.ParseIP(e.Client); ip != nil && ip.To4() == nil {
		q.Family = "ip6"
	}
	if e.DNSSEC {
		q.DNSSEC = "Secure"
	}
	if ms, err := strconv.ParseFloat(e.ElapsedMS, 64); err == nil {
		q.ResolveTimeMS = ms
	}
	if len(e.Answer) > 0 {
		q.TTL = e.Answer[0].TTL
	}
	if len(e.Rules) > 0 {
		q.Rule = e.Rules[0].Text
		q.Blocklist = lists[e.Rules[0].FilterListID]
	}

	switch {
	case strings.HasPrefix(e.Reason, "Filtered"):
		q.Action, q.Source = "Block", "Blocklist"
	case strings.HasPrefix(e.Reason, "Rewrite"):
		q.Source = "Rewrite"
	case e.Cached:
		q.Source = "Cache"
	default:
		q.Source = "Upstream"
	}
	return q
}

// pinnedTransport trusts exactly one certificate: AdGuard Home's is
// self-signed, so it is checked against its fingerprint instead of a CA.
func pinnedTransport(pin string) (*http.Transport, error) {
	want, err := hex.DecodeString(strings.ReplaceAll(strings.ToLower(pin), ":", ""))
	if err != nil || len(want) != sha256.Size {
		return nil, errors.New("certificate pin is not a hex sha256")
	}

	return &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The chain isn't verified against a CA; VerifyConnection checks the pin.
			InsecureSkipVerify: true, //nolint:gosec // replaced by the pin check below
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("adguard sent no certificate")
				}
				got := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if !strings.EqualFold(hex.EncodeToString(got[:]), hex.EncodeToString(want)) {
					return errors.New("adguard certificate does not match the pinned fingerprint")
				}
				return nil
			},
		},
	}, nil
}
