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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	traefikv1alpha1 "github.com/chtc/fleet-manager/internal/traefik/v1alpha1"
)

// DeploymentReconciler reconciles a Deployment object
type DeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Annotation that must be set for deployments that are personal APs
const AP_TAG = "chtc.wisc.edu/personal-ap"

// Annotation set by the controller on deployments that have been assigned a port
const PORT_TAG = "chtc.wisc.edu/ap-port"

// Annotation set by the controller on created clusterIP services
const SERVICE_TAG = "chtc.wisc.edu/ap-clusterip-service"

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

func (r *DeploymentReconciler) findUnusedPort(ctx context.Context, namespace string, startPort, endPort int32) (int32, error) {
	var services corev1.ServiceList
	if err := r.List(
		ctx,
		&services,
		client.MatchingLabels{SERVICE_TAG: "true"},
		client.InNamespace(namespace),
	); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Services")
		return 0, err
	}
	for port := startPort; port <= endPort; port++ {
		inUse := slices.ContainsFunc(services.Items, func(s corev1.Service) bool {
			return s.Spec.Ports[0].Port == port
		})
		if !inUse {
			return port, nil
		}
	}
	return 0, fmt.Errorf("No open ports on range [%v, %v]", startPort, endPort)
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

	// find an unused port for the deployment
	newPort, err := r.findUnusedPort(ctx, req.Namespace, 9618, 9628)
	if err != nil {
		logger.Error(err, "Failed to find an unused port for Deployment")
		return ctrl.Result{}, err
	}

	svc, err := r.constructServiceForDeployment(ctx, deploy, newPort)
	if err != nil {
		logger.Error(err, "Failed to construct Service for Deployment")
		return ctrl.Result{}, err
	}

	// Create the Service in the cluster
	if err := r.Create(ctx, svc); err != nil {
		logger.Error(err, "Failed to create Service for Deployment")
		return ctrl.Result{}, err
	}

	route, err := r.constructIngressRouteTCPForDeployment(ctx, deploy, newPort)
	if err != nil {
		logger.Error(err, "Failed to construct IngressRouteTCP for Deployment")
		return ctrl.Result{}, err
	}

	// Create the IngressRouteTCP in the cluster
	if err := r.Create(ctx, route); err != nil {
		logger.Error(err, "Failed to create IngressRouteTCP for Deployment")
		return ctrl.Result{}, err
	}

	// Annotate the Deployment with the assigned port
	// TODO this isn't atomic, we can create the port and then fail to annotate the deployment
	patch := client.MergeFrom(deploy.DeepCopy())
	deploy.Annotations[PORT_TAG] = fmt.Sprintf("%d", newPort)
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
