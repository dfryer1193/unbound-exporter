# unbound-exporter

Copies OPNsense's Unbound DNS query log into VictoriaLogs, so DNS queries, and blocked ones especially, can be searched and graphed (in Grafana) for as long as VictoriaLogs keeps them, like the Pi-hole's query log. The router itself only serves its newest 1000 queries.

## How it works

Every poll interval it reads `GET /api/unbound/overview/searchQueries` from the router (OPNsense's DNS reporting, which needs the **Status: DNS Overview** privilege, nothing more) and writes the queries it hasn't written before to every configured VictoriaLogs server as JSON lines, on the stream `{source="opnsense-unbound", router="<router_name>"}`.

- **De-duplication.** The router's log has no ids and only second resolution, and identical queries can happen several times a second. The exporter remembers how many copies of each identical row it has written, for the minute before the newest row it has seen (OPNsense writes its log in batches, so rows can appear a little late).
- **Gaps.** If a full poll (1000 rows) is entirely newer than everything already seen, queries may have been missed and it logs a warning. Keep the poll interval well under the time the router's 1000 rows cover.
- **Replicas.** Each VictoriaLogs URL gets every query. One that can't be reached has its queries buffered in memory, up to `buffer_limit`, and caught up later.
- **Restarts** resume after the newest query any replica holds (looking back 24 hours). Queries the router logs late, in that same second, can be missed.
- **Readiness** (`/readyz`) is true while polls read the router and reach at least one replica. `/livez` and `/startupz` come from mjolnir.

Each query is written with these fields (all searchable): `client`, `family`, `type`, `domain` (without the trailing dot), `action` (`Pass`, `Block` or `Drop`), `blocked` (bool), `answer_source` (`Recursion`, `Cache`, `Local`, …), `blocklist`, `rcode`, `dnssec`, `policy`, `resolve_time_ms`, `ttl` and `router`.

## Configuration

Settings are a [Mimir](https://github.com/worldtree-industries/mimir) Config, `github.com/dfryer1193/unbound-exporter/service.ExporterConfig`, defined in [`service/exporter_config.json`](service/exporter_config.json) with their Defaults. The Exporter reads them on every poll, so Overrides apply without a restart. Regenerate the typed Config after editing the JSON with `make generate`.

The router's API key comes from the environment, never from Mimir: `OPNSENSE_API_KEY` and `OPNSENSE_API_SECRET`. Mimir itself needs `MIMIR_ENVIRONMENT` and object storage credentials (see Mimir's README).

## Deployment

Its manifests and the Grafana dashboard live in [talos-k8s-bootstrap](https://github.com/dfryer1193/talos-k8s-bootstrap), `kubernetes/unbound-exporter/`.

## Development

```bash
make generate   # after editing service/exporter_config.json
make lint test build
```
