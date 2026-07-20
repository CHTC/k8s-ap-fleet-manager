/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	traefikv1alpha1 "github.com/chtc/fleet-manager/internal/traefik/v1alpha1"
)

// DeploymentReconciler reconciles a Deployment object
type DeploymentReconciler struct {
	CollectorClient
	client.Client
	Scheme *runtime.Scheme
}

// Annotation that must be set for deployments that are personal APs
const AP_TAG = "chtc.wisc.edu/personal-ap"

// Annotation set by the controller on deployments that have been assigned a port
const PORT_TAG = "chtc.wisc.edu/ap-port"

// Label set by the controller on created clusterIP services
const SERVICE_TAG = "chtc.wisc.edu/ap-clusterip-service"

// Label set by the controller on created IngressRouteTCPs
const INGRESSROUTETCP_TAG = "chtc.wisc.edu/ap-ingressroutetcp"

// To get atomicity on created Service port allocations, name the service after their port
// to trigger name collisions on double allocation
const PORT_SERVICE_NAME = "ap-port-%v"

// Name (and entryPoint) given to the IngressRouteTCP created for a personal AP's port
const INGRESS_ROUTE_TCP_NAME = "personal-ap-%v"

func (r *DeploymentReconciler) constructServiceForDeployment(ctx context.Context, deploy *appsv1.Deployment, port int32) (*corev1.Service, error) {
	// Base service spec: target the deployment's pods using its template labels, and expose the specified port
	svc := &corev1.Service{
		ObjectMeta: ctrl.ObjectMeta{
			Name:      fmt.Sprintf(PORT_SERVICE_NAME, port),
			Namespace: deploy.Namespace,
			Labels: map[string]string{
				SERVICE_TAG: "true",
			},
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Selector: deploy.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "htcondor",
					Port:       port,
					TargetPort: intstr.FromInt32(port),
				},
			},
		},
	}

	// Set the deployment as owner of the service

	if err := ctrl.SetControllerReference(deploy, svc, r.Scheme); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to set controller reference for Service")
		return nil, err
	}

	return svc, nil
}

func (r *DeploymentReconciler) constructIngressRouteTCPForDeployment(ctx context.Context, deploy *appsv1.Deployment, port int32) (*traefikv1alpha1.IngressRouteTCP, error) {
	name := fmt.Sprintf(INGRESS_ROUTE_TCP_NAME, port)

	// Route all TCP traffic on this port's dedicated entryPoint to the Service already
	// created for the deployment on that same port.
	route := &traefikv1alpha1.IngressRouteTCP{
		ObjectMeta: ctrl.ObjectMeta{
			Name:      name,
			Namespace: deploy.Namespace,
			Labels: map[string]string{
				INGRESSROUTETCP_TAG: "true",
			},
		},
		Spec: traefikv1alpha1.IngressRouteTCPSpec{
			EntryPoints: []string{name},
			Routes: []traefikv1alpha1.RouteTCP{
				{
					Match: "HostSNI(`*`)",
					Services: []traefikv1alpha1.ServiceTCP{
						{
							Name: fmt.Sprintf(PORT_SERVICE_NAME, port),
							Port: port,
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(deploy, route, r.Scheme); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to set controller reference for IngressRouteTCP")
		return nil, err
	}

	return route, nil
}

func (r *DeploymentReconciler) listServices(ctx context.Context, namespace string) ([]corev1.Service, error) {
	var services corev1.ServiceList
	if err := r.List(
		ctx,
		&services,
		client.MatchingLabels{SERVICE_TAG: "true"},
		client.InNamespace(namespace),
	); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Services")
		return nil, err
	}
	return services.Items, nil
}

func (r *DeploymentReconciler) listIngressRouteTCPs(ctx context.Context, namespace string) ([]traefikv1alpha1.IngressRouteTCP, error) {
	var routes traefikv1alpha1.IngressRouteTCPList
	if err := r.List(
		ctx,
		&routes,
		client.MatchingLabels{INGRESSROUTETCP_TAG: "true"},
		client.InNamespace(namespace),
	); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list IngressRouteTCPs")
		return nil, err
	}
	return routes.Items, nil
}

func findUnusedPort(services []corev1.Service, startPort, endPort int32) (int32, error) {
	for port := startPort; port <= endPort; port++ {
		inUse := slices.ContainsFunc(services, func(s corev1.Service) bool {
			return s.Spec.Ports[0].Port == port
		})
		if !inUse {
			return port, nil
		}
	}
	return 0, fmt.Errorf("No open ports on range [%v, %v]", startPort, endPort)
}

func findServiceOwnedByDeployment(services []corev1.Service, deployName string) (*corev1.Service, bool) {
	idx := slices.IndexFunc(services, func(s corev1.Service) bool {
		return slices.ContainsFunc(s.GetOwnerReferences(), func(o v1.OwnerReference) bool {
			return o.Kind == "Deployment" && o.Name == deployName
		})
	})

	if idx == -1 {
		return nil, false
	}
	return &services[idx], true
}

func findIngressRouteOwnedByDeployment(ingresses []traefikv1alpha1.IngressRouteTCP, deployName string) (*traefikv1alpha1.IngressRouteTCP, bool) {
	idx := slices.IndexFunc(ingresses, func(i traefikv1alpha1.IngressRouteTCP) bool {
		return slices.ContainsFunc(i.GetOwnerReferences(), func(o v1.OwnerReference) bool {
			return o.Kind == "Deployment" && o.Name == deployName
		})
	})

	if idx == -1 {
		return nil, false
	}
	return &ingresses[idx], true
}

func (r *DeploymentReconciler) setupService(ctx context.Context, deploy *appsv1.Deployment) (svc *corev1.Service, err error) {
	logger := logf.FromContext(ctx)
	// Check if a Service already exists for this Deployment
	services, err := r.listServices(ctx, deploy.Namespace)
	if err != nil {
		logger.Error(err, "Failed to list Services")
		return
	}

	if svc, found := findServiceOwnedByDeployment(services, deploy.Name); found {
		return svc, nil
	}

	logger.Info("Service does not yet exist for Deployment, attempting to create")

	// find an unused port for the deployment
	newPort, err := findUnusedPort(services, 9618, 9628)
	if err != nil {
		logger.Error(err, "Failed to find an unused port for Deployment")
		return
	}

	svc, err = r.constructServiceForDeployment(ctx, deploy, newPort)
	if err != nil {
		logger.Error(err, "Failed to construct Service for Deployment")
		return
	}

	// Create the Service in the cluster
	if err = r.Create(ctx, svc); err != nil {
		logger.Error(err, "Failed to create Service for Deployment")
		return
	}

	return
}

func (r *DeploymentReconciler) setupIngressRouteTCP(ctx context.Context, deploy *appsv1.Deployment, port int32) (route *traefikv1alpha1.IngressRouteTCP, err error) {
	logger := logf.FromContext(ctx)
	// Check if an IngressRouteTCP already exists for this Deployment
	ingressRoutes, err := r.listIngressRouteTCPs(ctx, deploy.Namespace)
	if err != nil {
		logger.Error(err, "Failed to list IngressRouteTCPs")
		return
	}

	if route, found := findIngressRouteOwnedByDeployment(ingressRoutes, deploy.Name); found {
		return route, nil
	}
	//
	// Create a new IngressRouteTCP for the Deployment
	route, err = r.constructIngressRouteTCPForDeployment(ctx, deploy, port)
	if err != nil {
		logger.Error(err, "Failed to construct IngressRouteTCP for Deployment")
		return
	}

	// Create the IngressRouteTCP in the cluster
	if err = r.Create(ctx, route); err != nil {
		logger.Error(err, "Failed to create IngressRouteTCP for Deployment")
		return
	}
	return
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *DeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling Deployment", "namespace", req.Namespace, "name", req.Name)

	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, req.NamespacedName, deploy); err != nil {
		logger.Error(err, "unable to fetch Deployment")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if deploy.DeletionTimestamp != nil {
		logger.Info("Deployment is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Check if the Deployment has the AP_TAG label
	if tag, ok := deploy.Annotations[AP_TAG]; !ok || tag != "true" {
		logger.Info("Deployment does not have the AP_TAG label, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Check if the Deployment has been assigned a port by looking for the "port" annotation
	if port, ok := deploy.Annotations[PORT_TAG]; ok && port != "" {
		logger.Info("Deployment already has a port assigned, skipping reconciliation", "port", port)
		return ctrl.Result{}, nil
	}

	svc, err := r.setupService(ctx, deploy)
	if err != nil {
		logger.Error(err, "Failed to setup Service for Deployment")
		return ctrl.Result{}, err
	}

	// Key remaining port-facing actions based on the service's chosen port
	svcPort := svc.Spec.Ports[0].Port

	if _, err = r.setupIngressRouteTCP(ctx, deploy, svcPort); err != nil {
		logger.Error(err, "Failed to setup IngressRouteTCP for Deployment")
		return ctrl.Result{}, err
	}

	// External side effect: Advertise the decided port to a local HTCondor collector
	// Do not mark port assignment as complete until advertisement succeeds, so that
	// the Deployment will be retried if the advertisement fails
	if err = r.CollectorClient.AdvertiseDeploymentPort(ctx, req.NamespacedName, svcPort); err != nil {
		logger.Error(err, "Failed to advertise Deployment port to local collector")
		return ctrl.Result{}, err
	}

	// Annotate the Deployment with the assigned port
	// TODO this isn't atomic, we can create the port and then fail to annotate the deployment
	patch := client.MergeFrom(deploy.DeepCopy())
	deploy.Annotations[PORT_TAG] = fmt.Sprintf("%d", svcPort)
	if err := r.Patch(ctx, deploy, patch); err != nil {
		logger.Error(err, "Failed to annotate Deployment with assigned port")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Named("deployment").
		Complete(r)
}
