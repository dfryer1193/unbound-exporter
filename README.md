# unbound-exporter

Copies the router's DNS query log into VictoriaLogs, so DNS queries, blocked ones especially, can be searched and graphed in Grafana for as long as VictoriaLogs keeps them, like the Pi-hole's query log.

The router (OPNsense) runs AdGuard Home on port 53, which filters and logs every query and forwards the rest to Unbound. Since v0.2.0 the exporter reads **AdGuard Home's** query log; before that, AdGuard wasn't there and it read Unbound's (OPNsense DNS reporting). The name stayed.

## How it works

Every poll interval it pages back through `GET /control/querylog` on AdGuard Home's API (newest first, `older_than` paging) to a minute before the newest query it has already written, and writes the queries it hasn't written before to every configured VictoriaLogs server as JSON lines, on the stream `{source="opnsense-unbound", router="<router_name>"}` (the stream name predates AdGuard; kept so history and the dashboard carry on).

- **De-duplication.** Log entries have no ids, and a slow query is logged after faster ones that started later, so polls overlap by a minute. The exporter remembers how many copies of each identical entry it has written in that minute.
- **Catching up.** AdGuard Home keeps its log for 30 days, so after an outage a poll pages back to where the exporter left off, up to 50,000 queries a poll; anything beyond that is skipped and logged.
- **Replicas.** Each VictoriaLogs URL gets every query. One that can't be reached has its queries buffered in memory, up to `buffer_limit`, and caught up later.
- **Restarts** resume after the newest query any replica holds (looking back 24 hours); with nothing written yet, it starts with the last 10 minutes.
- **Readiness** (`/readyz`) is true while polls read AdGuard Home and reach at least one replica. `/livez` and `/startupz` come from mjolnir.

Each query is written with these fields (all searchable): `client` (name, else IP), `client_ip`, `family`, `type`, `domain`, `action` (`Pass` or `Block`), `blocked` (bool), `reason` (AdGuard Home's, e.g. `FilteredBlackList`), `answer_source` (`Upstream`, `Cache`, `Blocklist`, `Rewrite`), `blocklist` (the filter list that matched, or `Custom rules`), `rule`, `rcode`, `dnssec`, `resolve_time_ms`, `ttl` and `router`.

## Configuration

Settings are a [Mimir](https://github.com/worldtree-industries/mimir) Config, `github.com/dfryer1193/unbound-exporter/service.ExporterConfig`, defined in [`service/exporter_config.json`](service/exporter_config.json) with their Defaults. The Exporter reads them on every poll, so Overrides apply without a restart. Regenerate the typed Config after editing the JSON with `make generate`.

AdGuard Home's certificate is self-signed, so it is trusted by its sha256 pin (`adguard_cert_sha256`). The AdGuard Home login comes from the environment, never from Mimir: `ADGUARD_USERNAME` and `ADGUARD_PASSWORD`. Mimir itself needs `MIMIR_ENVIRONMENT` and object storage credentials (see Mimir's README).

## Deployment

Its manifests and the Grafana dashboard live in [talos-k8s-bootstrap](https://github.com/dfryer1193/talos-k8s-bootstrap), `kubernetes/unbound-exporter/`.

## Development

```bash
make generate   # after editing service/exporter_config.json
make lint test build
```
