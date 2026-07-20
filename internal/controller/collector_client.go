package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/PelicanPlatform/classad/classad"
	"github.com/bbockelm/cedar/commands"
	htcondor "github.com/bbockelm/golang-htcondor"
	htConfig "github.com/bbockelm/golang-htcondor/config"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type CollectorClient interface {
	// AdvertiseDeploymentPort advertises the deployment's chosen port to the local HTCondor collector.
	AdvertiseDeploymentPort(ctx context.Context, deployment types.NamespacedName, port int32) error

	// InvalidateDeploymentPort invalidates the deployment's advertised port in the local HTCondor collector.
	InvalidateDeploymentPort(ctx context.Context, deployment types.NamespacedName) error
}

type _collectorClient struct {
	collector *htcondor.Collector
}

// initCollector initializes the HTCondor collector client if it hasn't been initialized yet.
func (c *_collectorClient) initCollector(ctx context.Context) (*htcondor.Collector, error) {
	if c.collector != nil {
		return c.collector, nil
	}
	logger := logf.FromContext(ctx)
	config, err := htConfig.New()
	if err != nil {
		logger.Error(err, "failed to load HTCondor configuration")
		return nil, err
	}

	// We need to contact the local collector via its exact COLLECTOR_HOST name for FS auth to work
	collectorName, ok := config.Get("COLLECTOR_HOST")
	if !ok {
		logger.Error(err, "failed to get COLLECTOR_HOST from HTCondor configuration")
		return nil, err
	}
	c.collector = htcondor.NewCollector(collectorName)
	return c.collector, nil
}

// AdvertiseDeploymentPort advertises the deployment's chosen port to the local HTCondor collector.
func (c *_collectorClient) AdvertiseDeploymentPort(ctx context.Context, deployment types.NamespacedName, port int32) error {
	logger := logf.FromContext(ctx)
	logger.Info("advertising deployment port to local collector", "deployment", deployment, "port", port)

	collector, err := c.initCollector(ctx)
	if err != nil {
		logger.Error(err, "failed to initialize HTCondor collector")
		return err
	}

	ad := classad.New()
	ad.Set("MyType", "Generic")
	ad.Set("Name", deployment.Name)
	ad.Set("Namespace", deployment.Namespace)
	ad.Set("Port", port)

	adCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := collector.Advertise(adCtx, ad, nil); err != nil {
		logger.Error(err, "failed to advertise deployment port")
		return err
	}
	return nil
}

func (c *_collectorClient) InvalidateDeploymentPort(ctx context.Context, deployment types.NamespacedName) error {
	logger := logf.FromContext(ctx)
	logger.Info("invalidating deployment port ad in local collector", "deployment", deployment)

	collector, err := c.initCollector(ctx)
	if err != nil {
		logger.Error(err, "failed to initialize HTCondor collector")
		return err
	}

	inv := classad.New()
	inv.Set("MyType", "Query")
	inv.Set("TargetType", "Generic")
	inv.Set("Name", deployment.Name)
	inv.Set("Requirements", fmt.Sprintf(`Name == "%s"`, deployment.Name))

	if err := collector.Advertise(ctx, inv, &htcondor.AdvertiseOptions{
		Command: commands.INVALIDATE_ADS_GENERIC,
	}); err != nil {
		logger.Error(err, "Failed to send invalidate query")
		return err
	}
	return nil
}

func NewCollectorClient() CollectorClient {
	return &_collectorClient{}
}
