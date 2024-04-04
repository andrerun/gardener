// Copyright 2024 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/*
Package bipa implements the "BilinearPodAutoscaler" - an autoscaling setup for kube-apiserver comprising an independently
driven horizontal and vertical pod autoscalers.

The HPA is driven by an application-specific load metric, based on the rate of requests made to the server. The goal of
HPA is to determine a rough value for the minimal number of replicas guaranteed to suffice for processing the load. That
rough estimate comes with a substantial safety margin which is offset by VPA shrinking the replicas as necessary (see below).

The VPA element is a typical VPA setup acting on both CPU and memory. The goal of VPA is to vertically adjust the
replicas provided based on HPA's rough estimate, to a scale that best matches the actual need for compute power.
*/
package bipa

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/component/gardenercustommetrics"
	"github.com/gardener/gardener/pkg/controllerutils"
	gardenerutils "github.com/gardener/gardener/pkg/utils/gardener"
	kubernetesutils "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
)

// DesiredStateParameters contains all configurable options of the BilinearPodAutoscaler's desired state
type DesiredStateParameters struct {
	// The name of the kube-apiserver container inside the kube-apiserver pod
	ContainerNameApiserver string
	// If true, reconciliation will strive for a working deployment on the server. If false, reconciliation will remove
	// any elements of a previously existing deployment on the server.
	IsEnabled bool
	// MinReplicaCount and MaxReplicaCount control the horizontal scaling range
	MaxReplicaCount int32
	// MinReplicaCount and MaxReplicaCount control the horizontal scaling range
	MinReplicaCount int32
}

// BilinearPodAutoscaler implements an autoscaling setup for kube-apiserver comprising an independently driven horizontal
// and vertical pod autoscalers. For further overview of the autoscaling behavior, see package bipa.
//
// The underlying implementation is an arrangement of k8s resources deployed as part of the target shoot's control plane.
// A BilinearPodAutoscaler object itself is stateless. As far as state is concerned, it is nothing more than a handle,
// pointing to the server-side setup.
type BilinearPodAutoscaler struct {
	deploymentNameApiserver string // Also used as name for the underlying HPA and VPA resources
	namespace               string
}

// NewBilinearPodAutoscaler creates a local handle object, pointed at a server-side BilinearPodAutoscaler instance
// of interest (either already existing, or desired). A BilinearPodAutoscaler lives in a shoot namespace,
// specified by the namespace parameter. The resulting object can be used to manipulate the server-side setup.
func NewBilinearPodAutoscaler(namespace string, deploymentNameApiserver string) *BilinearPodAutoscaler {
	return &BilinearPodAutoscaler{
		namespace:               namespace,
		deploymentNameApiserver: deploymentNameApiserver,
	}
}

// DeleteFromServer removes all BilinearPodAutoscaler artefacts from the shoot control plane.
// The seedClient parameter specifies a connection to the server hosting said control plane.
func (bipa *BilinearPodAutoscaler) DeleteFromServer(ctx context.Context, seedClient client.Client) error {
	baseErrorMessage :=
		fmt.Sprintf("An error occurred while deleting BilinearPodAutoscaler '%s' in namespace '%s'",
			bipa.deploymentNameApiserver,
			bipa.namespace)

	if err := managedresources.DeleteForShoot(ctx, seedClient, bipa.namespace, gardenercustommetrics.ComponentName); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to delete the ManagedResource '%s', which serves as envelope for delivering the resoures from "+
			"seed to shoot. The error message reported by the underlying operation follows: %w",
			gardenercustommetrics.ComponentName,
			err)
	}

	if err := client.IgnoreNotFound(kubernetesutils.DeleteObject(ctx, seedClient, bipa.makeEmptyHPA())); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to delete the HPA which is part of the BilinearPodAutoscaler from the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	if err := client.IgnoreNotFound(kubernetesutils.DeleteObject(ctx, seedClient, bipa.makeEmptyVPA())); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to delete the VPA which is part of the BilinearPodAutoscaler from the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	shootAccessSecret := bipa.makeShootAccessSecret()
	if err := kubernetesutils.DeleteObjects(ctx, seedClient, shootAccessSecret.Secret); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to delete the secret '%s' from the server. The purpose of that secret is to provide shoot "+
			"access to the gardener-custom-metrics component, which is deployed as part of the BilinearPodAutoscaler. "+
			"The error message reported by the underlying operation follows: %w",
			shootAccessSecret.Secret.Name,
			err)
	}

	return nil
}

// Reconcile brings the server-side BilinearPodAutoscaler setup in compliance with the desired state specified by the
// operation's parameters.
// The seedClient parameter specifies a connection to the server hosting said control plane.
// The 'parameters' parameter specifies the desired state that is to be applied upon the server-side autoscaler setup.
func (bipa *BilinearPodAutoscaler) Reconcile(
	ctx context.Context, seedClient client.Client, parameters *DesiredStateParameters) error {
	baseErrorMessage :=
		fmt.Sprintf("An error occurred while reconciling BilinearPodAutoscaler '%s' in namespace '%s'",
			bipa.deploymentNameApiserver,
			bipa.namespace)

	if !parameters.IsEnabled {
		if err := bipa.DeleteFromServer(ctx, seedClient); err != nil {
			return fmt.Errorf(baseErrorMessage+
				" - failed to bring the BilinearPodAutoscaler on the server to a fully disabled state. "+
				"The error message reported by the underlying operation follows: %w",
				err)
		}
		return nil
	}

	if err := bipa.reconcileHPA(ctx, seedClient, parameters.MinReplicaCount, parameters.MaxReplicaCount); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to reconcile the HPA which is part of the BilinearPodAutoscaler on the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	if err := bipa.reconcileVPA(ctx, seedClient, parameters.ContainerNameApiserver, parameters.MinReplicaCount); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to reconcile the VPA which is part of the BilinearPodAutoscaler on the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	// Create shoot access token for metrics scraping by gardener-custom-metrics
	shootAccessSecret := bipa.makeShootAccessSecret()
	if err := shootAccessSecret.Reconcile(ctx, seedClient); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to create the shoot access token secret '%s' on the server. "+
			"That secret is needed by the gardener-custom-metrics component in order to scrape metrics from the "+
			"shoot's kube-apiserver. "+
			"The error message reported by the underlying operation follows: %w",
			shootAccessSecret.Secret.Name,
			err)
	}

	if err := bipa.reconcileAppResources(ctx, shootAccessSecret.ServiceAccountName, seedClient); err != nil {
		return err
	}

	return nil
}

//#region Private implementation

// GetHPAName returns the name of BilinearPodAutoscaler's server-side HPA.
func (bipa *BilinearPodAutoscaler) GetHPAName() string {
	return bipa.deploymentNameApiserver + "-bipa"
}

// GetVPAName returns the name of BilinearPodAutoscaler's server-side VPA.
func (bipa *BilinearPodAutoscaler) GetVPAName() string {
	return bipa.deploymentNameApiserver + "-bipa"
}

// Returns an empty HPA object pointing to the server-side HPA, which is part of this BilinearPodAutoscaler
func (bipa *BilinearPodAutoscaler) makeEmptyHPA() *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: bipa.GetHPAName(), Namespace: bipa.namespace},
	}
}

// Returns an empty VPA object pointing to the server-side VPA, which is part of this BilinearPodAutoscaler
func (bipa *BilinearPodAutoscaler) makeEmptyVPA() *vpaautoscalingv1.VerticalPodAutoscaler {
	return &vpaautoscalingv1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: bipa.GetVPAName(), Namespace: bipa.namespace},
	}
}

// Reconciles the HPA resource which is part of the BilinearPodAutoscaler.
// minReplicaCount and maxReplicaCount control the horizontal scaling range.
func (bipa *BilinearPodAutoscaler) reconcileHPA(
	ctx context.Context, seedClient client.Client, minReplicaCount int32, maxReplicaCount int32) error {
	hpa := bipa.makeEmptyHPA()
	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, seedClient, hpa, func() error {
		hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
			Name:       bipa.deploymentNameApiserver,
		}
		hpa.Spec.Behavior = &autoscalingv2.HorizontalPodAutoscalerBehavior{
			ScaleDown: &autoscalingv2.HPAScalingRules{
				StabilizationWindowSeconds: pointer.Int32(900),
			},
		}

		lvalue300 := resource.MustParse("300")
		// This is where we direct HPA to use the metric supplied by the gardener-custom-metrics component
		hpaMetrics := []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: "shoot:apiserver_request_total:sum"},
					Target: autoscalingv2.MetricTarget{AverageValue: &lvalue300, Type: autoscalingv2.AverageValueMetricType},
				},
			},
		}
		hpa.Spec.Metrics = hpaMetrics
		hpa.Spec.MinReplicas = &minReplicaCount
		hpa.Spec.MaxReplicas = maxReplicaCount
		hpa.ObjectMeta.Labels = map[string]string{v1beta1constants.LabelRole: v1beta1constants.LabelAPIServer + "-hpa"}

		return nil
	})

	if err != nil {
		return fmt.Errorf("An error occurred while reconciling the '%s' HPA which is part of the BilinearPodAutoscaler "+
			"in namespace '%s' - failed to apply the desired configuration values to the server-side object. "+
			"The error message reported by the underlying operation follows: %w",
			bipa.GetHPAName(),
			bipa.namespace,
			err)
	}

	return nil
}

// Reconciles the VPA resource which is part of the BilinearPodAutoscaler
func (bipa *BilinearPodAutoscaler) reconcileVPA(ctx context.Context, seedClient client.Client, containerNameApiserver string, minReplicaCount int32) error {
	vpa := bipa.makeEmptyVPA()
	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, seedClient, vpa, func() error {
		vpa.Spec.Recommenders = nil
		vpa.Spec.TargetRef = &autoscalingv1.CrossVersionObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
			Name:       bipa.deploymentNameApiserver,
		}
		updateModeAutoAsLvalue := vpaautoscalingv1.UpdateModeAuto
		vpa.Spec.UpdatePolicy = &vpaautoscalingv1.PodUpdatePolicy{
			MinReplicas: &minReplicaCount,
			UpdateMode:  &updateModeAutoAsLvalue,
		}
		vpa.Spec.ResourcePolicy = &vpaautoscalingv1.PodResourcePolicy{
			ContainerPolicies: makeDefaultVPAResourcePolicies(containerNameApiserver),
		}
		vpa.ObjectMeta.Labels = map[string]string{v1beta1constants.LabelRole: v1beta1constants.LabelAPIServer + "-vpa"}

		return nil
	})

	if err != nil {
		return fmt.Errorf("An error occurred while reconciling the '%s' VPA which is part of the BilinearPodAutoscaler "+
			"in namespace '%s' - failed to apply the desired configuration values to the server-side object. "+
			"The error message reported by the underlying operation follows: %w",
			bipa.GetVPAName(),
			bipa.namespace,
			err)
	}

	return nil
}

// Creates a list of VPA ContainerResourcePolicy objects, initialised with default settings
func makeDefaultVPAResourcePolicies(containerNameApiserver string) []vpaautoscalingv1.ContainerResourcePolicy {
	scalingModeAutoAsLvalue := vpaautoscalingv1.ContainerScalingModeAuto
	controlledValuesRequestsOnlyAsLvalue := vpaautoscalingv1.ContainerControlledValuesRequestsOnly

	return []vpaautoscalingv1.ContainerResourcePolicy{
		{
			ContainerName: containerNameApiserver,
			Mode:          &scalingModeAutoAsLvalue,
			MinAllowed: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("400M"),
			},
			MaxAllowed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("25G"),
			},
			ControlledValues: &controlledValuesRequestsOnlyAsLvalue,
		},
	}
}

// Creates an empty shoot access secret. The name of the resulting object is a fixed function of the input parameters,
// so two instances created with the same parameters point to the same server side object.
func (bipa *BilinearPodAutoscaler) makeShootAccessSecret() *gardenerutils.AccessSecret {
	return gardenerutils.
		NewShootAccessSecret(gardenercustommetrics.ComponentName, bipa.namespace).
		WithSecretLabels(map[string]string{"name": "shoot-access-gardener-custom-metrics"})
}

// reconcileAppResources reconciles those bipa resources which belong inside the shoot cluster. This function does not
// reconcile deletion.
func (bipa *BilinearPodAutoscaler) reconcileAppResources(ctx context.Context, serviceAccountName string, seedClient client.Client) error {
	var (
		baseErrorMessage = fmt.Sprintf(
			"An error occurred while applying the BilinearPodAutoscaler resources which belong inside shoot '%s'",
			bipa.namespace)
		registry = managedresources.NewRegistry(kubernetes.ShootScheme, kubernetes.ShootCodec, kubernetes.ShootSerializer)

		clusterRole = &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gardener.cloud:monitoring:gardener-custom-metrics-target",
			},
			Rules: []rbacv1.PolicyRule{
				{
					NonResourceURLs: []string{"/metrics"},
					Verbs:           []string{"get"},
				},
			},
		}
		clusterRoleBinding = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "gardener.cloud:monitoring:gardener-custom-metrics-target",
				Annotations: map[string]string{resourcesv1alpha1.DeleteOnInvalidUpdate: "true"},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     clusterRole.Name,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      serviceAccountName,
				Namespace: metav1.NamespaceSystem,
			}},
		}
	)

	data, err := registry.AddAllAndSerialize(clusterRole, clusterRoleBinding)
	if err != nil {
		return fmt.Errorf(baseErrorMessage+" - failed to serialize the resources via managed resource registry. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	// The shoot app resources we deploy are used only by gardener-custom-metrics. Thus, we package them in a
	// managed resource named after gardener-custom-metrics instead of bipa itself.
	err = managedresources.CreateForShoot(
		ctx, seedClient, bipa.namespace, gardenercustommetrics.ComponentName, managedresources.LabelValueGardener, false, data)
	if err != nil {
		return fmt.Errorf(baseErrorMessage+" - failed to create the ManagedResource object which serves as "+
			"envelope for delivering the resoures from seed to shoot. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	return nil
}

//#endregion Private implementation
