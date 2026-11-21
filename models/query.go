package models

import "time"

// Query is one DNS query answered by Unbound, as OPNsense's DNS reporting
// records it.
type Query struct {
	Time          time.Time
	Client        string // client name if OPNsense resolved one, else its IP
	Family        string // ip4 or ip6
	Type          string // record type, e.g. A, AAAA
	Domain        string // queried name, with its trailing dot
	Action        string // Pass, Block or Drop
	Source        string // where the answer came from: Recursion, Cache, Local, ...
	Blocklist     string // the blocklist that matched, for a blocked query
	RCode         string // NOERROR, NXDOMAIN, ...
	DNSSEC        string // DNSSEC validation status
	Policy        string // uuid of the blocklist policy that matched
	ResolveTimeMS int64
	TTL           int64
}

// Blocked reports whether Unbound's blocklist answered the query.
func (q Query) Blocked() bool {
	return q.Action == "Block"
}
