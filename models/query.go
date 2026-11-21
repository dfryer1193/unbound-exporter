package models

import "time"

// Query is one DNS query answered by the router's AdGuard Home, as its query
// log records it.
type Query struct {
	Time          time.Time
	Client        string // client name if AdGuard Home resolved one, else its IP
	ClientIP      string
	Family        string // ip4 or ip6 (the client's address)
	Type          string // record type, e.g. A, AAAA
	Domain        string // queried name, without a trailing dot
	Action        string // Pass or Block
	Source        string // where the answer came from: Upstream, Cache, Blocklist, Rewrite
	Blocklist     string // the filter list whose rule matched, for a filtered query
	Rule          string // the rule that matched, if any
	Reason        string // AdGuard Home's reason, e.g. FilteredBlackList, NotFilteredNotFound
	RCode         string // NOERROR, NXDOMAIN, ...
	DNSSEC        string // Secure when the answer was DNSSEC-validated, else Insecure
	ResolveTimeMS float64
	TTL           int64 // of the first answer record, 0 if none
}

// Blocked reports whether a filter blocked the query.
func (q Query) Blocked() bool {
	return q.Action == "Block"
}
