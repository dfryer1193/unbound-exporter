package data

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dfryer1193/unbound-exporter/models"
)

// OPNsense reads Unbound's query log from an OPNsense router's API.
type OPNsense struct {
	key, secret string
	client      *http.Client
}

// OPNsenseSettings is where and how to reach the router.
type OPNsenseSettings struct {
	URL        string        // e.g. https://192.168.90.1
	CertSHA256 string        // hex sha256 of the router's (self-signed) certificate, DER
	Timeout    time.Duration // per request
}

// NewOPNsense returns a reader that authenticates with an API key and secret.
func NewOPNsense(key, secret string) *OPNsense {
	return &OPNsense{key: key, secret: secret, client: &http.Client{}}
}

// queryRow is one row of /api/unbound/overview/searchQueries.
type queryRow struct {
	Time          int64  `json:"time"`
	Client        string `json:"client"`
	Family        string `json:"family"`
	Type          string `json:"type"`
	Domain        string `json:"domain"`
	Action        string `json:"action"`
	Source        string `json:"source"`
	Blocklist     string `json:"blocklist"`
	RCode         string `json:"rcode"`
	ResolveTimeMS int64  `json:"resolve_time_ms"`
	DNSSEC        string `json:"dnssec_status"`
	TTL           int64  `json:"ttl"`
	Policy        string `json:"policy"`
}

// RecentQueries returns the newest queries OPNsense holds (it serves at most
// 1000), newest first.
func (o *OPNsense) RecentQueries(ctx context.Context, s OPNsenseSettings) ([]models.Query, error) {
	transport, err := pinnedTransport(s.CertSHA256)
	if err != nil {
		return nil, err
	}
	defer transport.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	url := strings.TrimSuffix(s.URL, "/") + "/api/unbound/overview/searchQueries"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(o.key, o.secret)

	client := *o.client
	client.Transport = transport
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query log: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query log: HTTP %d", resp.StatusCode)
	}

	var body struct {
		Rows []queryRow `json:"rows"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("query log: %w", err)
	}

	queries := make([]models.Query, 0, len(body.Rows))
	for _, r := range body.Rows {
		queries = append(queries, models.Query{
			Time:          time.Unix(r.Time, 0).UTC(),
			Client:        r.Client,
			Family:        r.Family,
			Type:          r.Type,
			Domain:        r.Domain,
			Action:        r.Action,
			Source:        r.Source,
			Blocklist:     r.Blocklist,
			RCode:         r.RCode,
			DNSSEC:        r.DNSSEC,
			Policy:        r.Policy,
			ResolveTimeMS: r.ResolveTimeMS,
			TTL:           r.TTL,
		})
	}
	return queries, nil
}

// pinnedTransport trusts exactly one certificate: the router's is self-signed,
// so it is checked against its fingerprint instead of a CA.
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
					return errors.New("router sent no certificate")
				}
				got := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if !strings.EqualFold(hex.EncodeToString(got[:]), hex.EncodeToString(want)) {
					return errors.New("router certificate does not match the pinned fingerprint")
				}
				return nil
			},
		},
	}, nil
}
