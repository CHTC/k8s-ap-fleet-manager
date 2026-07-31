package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	Name      string `json:"Name"`
	Namespace string `json:"Namespace"`
	Port      int64  `json:"Port"`
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
	cmd := exec.CommandContext(ctx, "condor_status", "-generic", "-json", "-const", fmt.Sprintf(`Name == "%s" && Namespace == "%s"`, lcfg.name, lcfg.namespace))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running condor_status: %w", err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return nil, fmt.Errorf("no output from condor_status for Name=%q Namespace=%q", lcfg.name, lcfg.namespace)
	}

	var rcfgs []fleetManRemoteConfig
	if err := json.Unmarshal(output, &rcfgs); err != nil {
		return nil, fmt.Errorf("parsing condor_status output: %w", err)
	}
	if len(rcfgs) == 0 {
		return nil, fmt.Errorf("no remote config found for Name=%q Namespace=%q", lcfg.name, lcfg.namespace)
	}
	if len(rcfgs) > 1 {
		return nil, fmt.Errorf("multiple remote configs found for Name=%q Namespace=%q", lcfg.name, lcfg.namespace)
	}
	return &rcfgs[0], nil
}

// fleetManRemoteConfigChanged compares the given fleetmanRemoteConfig with the current HTCondor config
// and returns true if any remote values differ from local.
func fleetManRemoteConfigChanged(rcfg *fleetManRemoteConfig, cfg *config.Config) (bool, error) {
	changeDetected := false

	cmd := exec.Command("condor_config_val", "SHARED_PORT_PORT")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("running condor_config_val: %w", err)
	}
	portTrimmed := strings.TrimSpace(string(output))
	currentPort, err := strconv.ParseInt(portTrimmed, 10, 64)
	if err != nil {
		return false, fmt.Errorf("parsing SHARED_PORT_PORT value %q: %w", portTrimmed, err)
	}
	if currentPort != rcfg.Port {
		changeDetected = true
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

	contents := fmt.Sprintf("# Generated by FleetManD -- do not edit by hand.\nSHARED_PORT_PORT = %d\n", rcfg.Port)
	return os.WriteFile(path, []byte(contents), 0644)
}

// reconfigureMaster sends condor_master a bare DC_RECONFIG: like DC_NOP, it
// carries no message payload -- the command itself is negotiated as part of
// the CEDAR security handshake, so ConnectAndAuthenticate succeeding is
// sending it.
func reconfigureMaster(ctx context.Context, d *daemon.Daemon) error {
	cmd := exec.CommandContext(ctx, "condor_reconfig")
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
