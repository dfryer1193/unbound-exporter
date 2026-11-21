// Command unbound-exporter copies OPNsense's Unbound DNS query log into
// VictoriaLogs, so queries (blocked ones especially) can be searched and graphed
// for as long as VictoriaLogs keeps them, instead of the few minutes the router
// serves. It is an HTTP server, a Deployable, only for mjolnir's probes
// (/livez, /readyz and /startupz), and drains gracefully on SIGTERM.
//
// Settings come from Mimir (service/exporter_config.json). The router API key
// comes from the environment, never from Mimir: OPNSENSE_API_KEY and
// OPNSENSE_API_SECRET.
package main

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/dfryer1193/mjolnir/health"
	"github.com/dfryer1193/mjolnir/router"
	"github.com/dfryer1193/mjolnir/utils/log"

	"github.com/dfryer1193/unbound-exporter/data"
	"github.com/dfryer1193/unbound-exporter/service"
)

// defaultPort is the port unbound-exporter listens on when PORT isn't set.
const defaultPort = "8080"

// drainBudget is the whole shutdown budget: half with readiness at 503, half
// for Shutdown. Keep it under the orchestrator's termination grace period.
const drainBudget = 10 * time.Second

func main() {
	os.Exit(run())
}

func run() int {
	lg := log.GetLogger("unbound-exporter")

	key, secret := os.Getenv("OPNSENSE_API_KEY"), os.Getenv("OPNSENSE_API_SECRET")
	if key == "" || secret == "" {
		lg.Error("OPNSENSE_API_KEY and OPNSENSE_API_SECRET must be set")
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := service.NewExporterConfig(ctx)
	exporter := service.NewExporter(cfg, data.NewOPNsense(key, secret), data.NewVictoriaLogs(), lg)
	go exporter.Run(ctx)

	r, err := router.New(router.Config{
		Health: health.Config{
			Started: health.AlwaysStarted,
			Ready:   func(context.Context) bool { return exporter.Healthy() },
		},
	})
	if err != nil {
		lg.Error("Failed to build router", "error", err)
		return 1
	}

	addr := net.JoinHostPort("", port())
	lg.Info("Server starting", "addr", addr)
	if err = r.Run(router.NewServer(addr, r), drainBudget); err != nil {
		lg.Error("Server failed", "error", err)
		return 1
	}
	lg.Info("Server stopped")
	return 0
}

// port is the port to listen on: PORT if set, otherwise defaultPort.
func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return defaultPort
}
