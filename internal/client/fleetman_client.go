package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bbockelm/cedar/client"
	"github.com/bbockelm/cedar/commands"
	htcondor "github.com/bbockelm/golang-htcondor"
	"github.com/bbockelm/golang-htcondor/config"
	"github.com/bbockelm/golang-htcondor/daemon"
	"github.com/bbockelm/golang-htcondor/logging"
)

const (
	collectorTimeout = 20 * time.Second
	reconfigTimeout  = 20 * time.Second
)

// fleetManLocalConfig holds the configuration values set in local HTCondor config for the FleetManD client.
type fleetManLocalConfig struct {
	name          string
	namespace     string
	collectorAddr string
	configPath    string
}

// fleetManRemoteConfig holds the configuration values set in the remote HTCondor collector for the FleetManD client.
type fleetManRemoteConfig struct {
	port int64
}

// newFleetManConfig reads the required HTCondor config values from cfg and returns a FleetManConfig.
// If any required value is missing, it returns an error.
func newFleetManConfig(cfg *config.Config) (*fleetManLocalConfig, error) {
	collectorAddr, ok := cfg.Get("FLEETMAND_COLLECTOR_ADDRESS")
	if !ok || collectorAddr == "" {
		return nil, fmt.Errorf("FLEETMAND_COLLECTOR_ADDRESS is not set")
	}
	configFile, ok := cfg.Get("FLEETMAND_CONFIG_FILE")
	if !ok || configFile == "" {
		return nil, fmt.Errorf("FLEETMAND_CONFIG_FILE is not set")
	}
	name, ok := cfg.Get("FLEETMAND_NAME")
	if !ok || name == "" {
		return nil, fmt.Errorf("FLEETMAND_NAME is not set")
	}
	namespace, ok := cfg.Get("FLEETMAND_NAMESPACE")
	if !ok || namespace == "" {
		return nil, fmt.Errorf("FLEETMAND_NAMESPACE is not set")
	}
	return &fleetManLocalConfig{
		name:          name,
		namespace:     namespace,
		collectorAddr: collectorAddr,
		configPath:    configFile,
	}, nil
}

// QueryFleetMan runs a single collector query, and -- if the discovered Port
// differs from the local SHARED_PORT_PORT -- writes the new config file and
// signals condor_master to pick it up.
func QueryFleetMan(ctx context.Context, d *daemon.Daemon) error {
	cfg := d.Config()
	log := d.Logger()

	qctx, cancel := context.WithTimeout(ctx, collectorTimeout)
	defer cancel()

	// Read local config values to find remote Fleet Manager address
	lcfg, err := newFleetManConfig(cfg)
	if err != nil {
		return err
	}

	// Query the remote Fleet Manager instance
	rcfg, err := queryFleetManRemoteConfig(qctx, lcfg)
	if err != nil {
		return err
	}

	// Check if local updates are needed based on the remote config values
	changed, err := fleetManRemoteConfigChanged(rcfg, cfg)
	if err != nil {
		return fmt.Errorf("checking for config changes: %w", err)
	}
	if !changed {
		log.Debug(logging.DestinationGeneral, "No changes detected in FleetManD remote config",
			"name", lcfg.name, "namespace", lcfg.namespace, "remoteConfig", rcfg)
		return nil
	}

	// Write a new config file and signal condor_master to reconfigure
	if err := writeFleetManRemoteConfig(lcfg.configPath, rcfg); err != nil {
		return fmt.Errorf("writing %s: %w", lcfg.configPath, err)
	}
	log.Info(logging.DestinationGeneral, "FleetManD updated",
		"name", lcfg.name, "namespace", lcfg.namespace, "remoteConfig", rcfg, "file", lcfg.configPath)

	rctx, rcancel := context.WithTimeout(ctx, reconfigTimeout)
	defer rcancel()
	if err := reconfigureMaster(rctx, d); err != nil {
		return fmt.Errorf("sending condor_reconfig: %w", err)
	}
	return nil
}

// queryFleetManRemoteConfig queries the collector for the generic ad matching Name/Namespace
// and returns a fleetmanRemoteConfig based on that ad's attributes.
func queryFleetManRemoteConfig(ctx context.Context, lcfg *fleetManLocalConfig) (*fleetManRemoteConfig, error) {
	collector := htcondor.NewCollector(lcfg.collectorAddr)

	constraint := fmt.Sprintf(`Name == "%s" && Namespace == "%s"`, lcfg.name, lcfg.namespace)
	ads, _, err := collector.QueryAdsWithOptions(ctx, "Generic", constraint, &htcondor.QueryOptions{
		Projection: []string{"Name", "Namespace", "Port"},
		Limit:      1,
	})
	if err != nil {
		return nil, fmt.Errorf("querying collector at %s: %w", lcfg.collectorAddr, err)
	}
	if len(ads) == 0 {
		return nil, fmt.Errorf("no generic ad found for Name=%q Namespace=%q", lcfg.name, lcfg.namespace)
	}

	port, ok := ads[0].EvaluateAttrInt("Port")
	if !ok {
		return nil, fmt.Errorf("generic ad for Name=%q Namespace=%q has no Port attribute", lcfg.name, lcfg.namespace)
	}
	return &fleetManRemoteConfig{port: port}, nil
}

// fleetManRemoteConfigChanged compares the given fleetmanRemoteConfig with the current HTCondor config
// and returns true if any remote values differ from local.
func fleetManRemoteConfigChanged(rcfg *fleetManRemoteConfig, cfg *config.Config) (bool, error) {
	changeDetected := false

	if current, ok := cfg.Get("SHARED_PORT_PORT"); ok {
		currentPort, err := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
		if err != nil {
			return false, err
		}
		if currentPort != rcfg.port {
			changeDetected = true
		}
	}

	return changeDetected, nil
}

// writeFleetManRemoteConfig overwrites path with a set of HTCondor config attributes
// derived from the given fleetmanRemoteConfig.
func writeFleetManRemoteConfig(path string, rcfg *fleetManRemoteConfig) error {
	// Try to create the parent directory for the path if it doesn't exist
	dir, _ := filepath.Split(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	contents := fmt.Sprintf("# Generated by FleetManD -- do not edit by hand.\nSHARED_PORT_PORT = %d\n", rcfg.port)
	return os.WriteFile(path, []byte(contents), 0644)
}

// reconfigureMaster sends condor_master a bare DC_RECONFIG: like DC_NOP, it
// carries no message payload -- the command itself is negotiated as part of
// the CEDAR security handshake, so ConnectAndAuthenticate succeeding is
// sending it.
func reconfigureMaster(ctx context.Context, d *daemon.Daemon) error {
	m := d.Master()
	if m == nil {
		return fmt.Errorf("not running under condor_master")
	}
	masterAddr := m.Address()

	secConfig, err := htcondor.GetSecurityConfigOrDefault(ctx, d.Config(), int(commands.DC_RECONFIG), "ADMINISTRATOR", masterAddr)
	if err != nil {
		return fmt.Errorf("building security config: %w", err)
	}

	cl, err := client.ConnectAndAuthenticate(ctx, masterAddr, secConfig)
	if err != nil {
		return fmt.Errorf("connecting to condor_master at %s: %w", masterAddr, err)
	}
	return cl.Close()
}
