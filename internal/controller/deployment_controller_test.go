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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MockCollectorClient is a testify/mock stand-in for CollectorClient, so tests
// don't reach out to a real HTCondor collector.
type MockCollectorClient struct {
	mock.Mock
}

func (m *MockCollectorClient) AdvertiseDeploymentPort(ctx context.Context, deployment types.NamespacedName, port int32) error {
	args := m.Called(ctx, deployment, port)
	return args.Error(0)
}

func (m *MockCollectorClient) InvalidateDeploymentPort(ctx context.Context, deployment types.NamespacedName) error {
	args := m.Called(ctx, deployment)
	return args.Error(0)
}

func makeDeployment(namespacedName types.NamespacedName) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespacedName.Name,
			Namespace: namespacedName.Namespace,
			Annotations: map[string]string{
				AP_TAG: "true",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": namespacedName.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": namespacedName.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
}

var _ = Describe("Deployment Controller", Ordered, func() {
	Context("When reconciling a resource", func() {

		ctx := context.Background()

		namespacedName := types.NamespacedName{
			Name:      "example-deployment",
			Namespace: "default",
		}

		namespacedName2 := types.NamespacedName{
			Name:      "example-deployment-2",
			Namespace: "default",
		}

		deployment := &appsv1.Deployment{}
		BeforeAll(func() {
			By("Creating two annotated Deployments")

			err := k8sClient.Get(ctx, namespacedName, deployment)
			if err != nil && errors.IsNotFound(err) {
				resource := makeDeployment(namespacedName)
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
				resource2 := makeDeployment(namespacedName2)
				Expect(k8sClient.Create(ctx, resource2)).To(Succeed())
			}
		})

		AfterAll(func() {
			// All cleanup is handled within tests
		})

		It("should successfully reconcile the first resource", func() {
			mockCollector := new(MockCollectorClient)
			mockCollector.On("AdvertiseDeploymentPort", ctx, namespacedName, int32(9618)).Return(nil)

			reconciler := &DeploymentReconciler{
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				CollectorClient: mockCollector,
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the Deployment has been annotated with a port and finalizer")
			updatedDeployment := &appsv1.Deployment{}
			err = k8sClient.Get(ctx, namespacedName, updatedDeployment)
			Expect(err).NotTo(HaveOccurred())
			portAnnotation, exists := updatedDeployment.Annotations[PORT_TAG]
			Expect(exists).To(BeTrue())
			Expect(portAnnotation).NotTo(BeEmpty())
			Expect(updatedDeployment.Finalizers).To(HaveLen(1))

			By("Checking if the corresponding Service has been created")
			expectedService := &corev1.Service{}
			svcName := types.NamespacedName{
				Name:      fmt.Sprintf(PORT_SERVICE_NAME, portAnnotation),
				Namespace: updatedDeployment.Namespace,
			}
			err = k8sClient.Get(ctx, svcName, expectedService)
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the deployment and service select on the same pods")
			Expect(updatedDeployment.Spec.Selector.MatchLabels).To(Equal(expectedService.Spec.Selector))

			By("Checking that the collector was advertised the assigned port")
			mockCollector.AssertExpectations(GinkgoT())
		})

		It("Should successfully reconcile the second resource on a different port", func() {
			mockCollector := new(MockCollectorClient)
			mockCollector.On("AdvertiseDeploymentPort", ctx, namespacedName2, int32(9619)).Return(nil)

			reconciler := &DeploymentReconciler{
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				CollectorClient: mockCollector,
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName2,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that two services now exist")
			var services corev1.ServiceList
			err = k8sClient.List(
				ctx,
				&services,
				client.MatchingLabels{SERVICE_TAG: "true"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(services.Items).To(HaveLen(2))
			Expect(services.Items[0].Spec.Ports[0].Port).NotTo(Equal(services.Items[1].Spec.Ports[0].Port))

			By("Checking that the collector was advertised the assigned port")
			mockCollector.AssertExpectations(GinkgoT())
		})

		It("Should invalidate the deployment's ad and remove its finalizer upon deletion", func() {
			mockCollector := new(MockCollectorClient)
			mockCollector.On("InvalidateDeploymentPort", ctx, namespacedName).Return(nil)

			By("Deleting the first deployment")
			deployment := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, namespacedName, deployment)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Delete(ctx, deployment)
			Expect(err).NotTo(HaveOccurred())
			reconciler := &DeploymentReconciler{
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				CollectorClient: mockCollector,
			}
			By("Re-reconciling the deployment to remove its finalizer")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the ad was removed from the collector")
			mockCollector.AssertExpectations(GinkgoT())

			By("Checking that the Deployment was deleted successfully after its finalizer was removed")
			err = k8sClient.Get(ctx, namespacedName, deployment)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})
