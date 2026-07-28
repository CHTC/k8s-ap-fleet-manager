// Command FleetManD is an HTCondor daemon built on the
// github.com/bbockelm/golang-htcondor/daemon framework, following the
// examples/noop_daemon template. Beyond the standard daemon lifecycle (DC_NOP
// / DC_RECONFIG / DC_OFF, condor_master readiness/keepalive, shared-port
// listener adoption), it runs a background loop that:
//
//  1. Polls a condor_collector for a generic ClassAd matching a configured
//     Name/Namespace.
//  2. Reads that ad's Port attribute.
//  3. If Port differs from the local config's SHARED_PORT_PORT, writes a new
//     condor config file setting SHARED_PORT_PORT to the discovered value and
//     sends condor_master a DC_RECONFIG so the pool picks it up.
//
// Config knobs (all under the FLEETMAND subsystem prefix, so
// FLEETMAND.<KNOB> / <LOCALNAME>.<KNOB> overrides work the same as any other
// daemon's config -- see config.Config.Get):
//
//	FLEETMAND_COLLECTOR_ADDRESS  collector to poll (host:port or sinful string)
//	FLEETMAND_NAME               Name to match in the generic ad query
//	FLEETMAND_NAMESPACE          Namespace to match in the generic ad query
//	FLEETMAND_CONFIG_FILE        condor config file this daemon writes
//	FLEETMAND_POLL_INTERVAL      seconds between polls (default 60)
//
// The written config file only takes effect once something in the pool's
// config chain (LOCAL_CONFIG_FILE/LOCAL_CONFIG_DIR) actually includes it --
// see the accompanying README.md for a worked example.
//
// Build: go build -o FleetManD .
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/bbockelm/cedar/commands"
	cedarserver "github.com/bbockelm/cedar/server"
	htcondor "github.com/bbockelm/golang-htcondor"
	"github.com/bbockelm/golang-htcondor/config"
	"github.com/bbockelm/golang-htcondor/daemon"
	"github.com/bbockelm/golang-htcondor/logging"
	"github.com/chtc/fleet-manager/internal/client"
)

const (
	defaultPollInterval = 60 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FleetManD:", err)
		os.Exit(1)
	}
}

func run() error {
	d, err := daemon.New(daemon.Options{Subsys: "FLEETMAND"})
	if err != nil {
		return err
	}
	log := d.Logger()

	ln, err := d.Listener(func() (net.Listener, error) {
		return (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	})
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	sec, err := htcondor.GetServerSecurityConfig(d.Config(), commands.DC_NOP, "DEFAULT")
	if err != nil {
		return fmt.Errorf("building security config: %w", err)
	}

	srv := cedarserver.New(sec)
	d.RegisterDefaultCommands(srv)

	// The poll loop runs alongside Serve rather than inside it: Serve owns the
	// command-port lifecycle (and only cancels its own internal copy of ctx on
	// shutdown), so this daemon cancels its own ctx once Serve returns to stop
	// the loop and wait for it to exit before returning.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		pollLoop(ctx, d)
	}()

	log.Info(logging.DestinationGeneral, "FleetManD starting",
		"listen", ln.Addr().String(), "under_master", d.UnderMaster())

	serveErr := d.Serve(ctx, ln, srv.Serve)
	cancel()
	<-pollDone
	return serveErr
}

// pollLoop polls once immediately, then on the configured interval, until ctx
// is canceled. Config is re-read every iteration.
func pollLoop(ctx context.Context, d *daemon.Daemon) {
	log := d.Logger()
	interval := getPollInterval(d.Config())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := client.QueryFleetMan(ctx, d); err != nil {
			log.Warn(logging.DestinationGeneral, "FleetManD poll failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Re-read config and reset ticker if the interval changed
			if next := getPollInterval(d.Config()); next != interval {
				interval = next
				ticker.Reset(interval)
			}
		}
	}
}

// getPollInterval returns the configured poll interval
func getPollInterval(cfg *config.Config) time.Duration {
	if v, ok := cfg.Get("FLEETMAND_POLL_INTERVAL"); ok {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultPollInterval
}
