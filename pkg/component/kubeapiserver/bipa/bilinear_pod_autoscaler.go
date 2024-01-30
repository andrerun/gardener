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
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
)

// DesiredStateParameters contains all configurable options of the BilinearPodAutoscaler's desired state
type DesiredStateParameters struct {
	// The name of the kube-apiserver container inside the kube-apiserver pod
	ContainerNameApiserver string
	// If true, reconciliation will strive for a working deployment on server. If false, reconciliation will remove
	// all elements of any existing deployment on the server.
	IsEnabled bool
	// MinReplicaCount and MaxReplicaCount control the horizontal scaling range
	MaxReplicaCount int32
	// MinReplicaCount and MaxReplicaCount control the horizontal scaling range
	MinReplicaCount int32
}

// BilinearPodAutoscaler implements an autoscaling setup for kube-apiserver comprising an independently driven horizontal and
// vertical pod autoscalers. For further overview of the autoscaling behavior, see package bipa.
//
// The underlying implementation is an arrangement of k8s resources deployed as part of the target shoot's control plane.
// A BilinearPodAutoscaler object itself is stateless. As far as state is concerned, it is nothing more than a handle,
// pointing to the server-side setup.
type BilinearPodAutoscaler struct {
	deploymentNameApiserver string // Also used as name for the underlying HPA and VPA resources
	namespaceName           string
}

// NewBilinearPodAutoscaler creates a local handle object, pointed at a server-side BilinearPodAutoscaler instance
// of interest (either already existing, or desired). A BilinearPodAutoscaler lives in a shoot namespace, and that
// namespace is specified by the namespaceName parameter. The resulting object can be used to manipulate the server-side
// setup.
func NewBilinearPodAutoscaler(namespaceName string, deploymentNameApiserver string) *BilinearPodAutoscaler {
	return &BilinearPodAutoscaler{
		namespaceName:           namespaceName,
		deploymentNameApiserver: deploymentNameApiserver,
	}
}

// DeleteFromServer removes all BilinearPodAutoscaler artefacts from the shoot control plane.
// The kubeClient parameter specifies a connection to the server hosting said control plane.
func (bipa *BilinearPodAutoscaler) DeleteFromServer(ctx context.Context, kubeClient client.Client) error {
	baseErrorMessage :=
		fmt.Sprintf("An error occurred while deleting BilinearPodAutoscaler '%s' in namespace '%s'",
			bipa.deploymentNameApiserver,
			bipa.namespaceName)

	// TODO: Andrey: P1: better error
	if err := managedresources.DeleteForShoot(ctx, kubeClient, bipa.namespaceName, gardenercustommetrics.ComponentName); err != nil {
		return err
	}

	if err := client.IgnoreNotFound(kutil.DeleteObject(ctx, kubeClient, bipa.makeHPA())); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to delete the HPA which is part of the BilinearPodAutoscaler from the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	if err := client.IgnoreNotFound(kutil.DeleteObject(ctx, kubeClient, bipa.makeVPA())); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to delete the VPA which is part of the BilinearPodAutoscaler from the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	return nil
}

// Reconcile brings the server-side BilinearPodAutoscaler setup in compliance with the desired state specified by the
// operation's parameters.
// The kubeClient parameter specifies a connection to the server hosting said control plane.
// The 'parameters' parameter specifies the desired state that is to be applied upon the server-side autoscaler setup.
func (bipa *BilinearPodAutoscaler) Reconcile(
	ctx context.Context, kubeClient client.Client, parameters *DesiredStateParameters) error {

	baseErrorMessage :=
		fmt.Sprintf("An error occurred while reconciling BilinearPodAutoscaler '%s' in namespace '%s'",
			bipa.deploymentNameApiserver,
			bipa.namespaceName)

	if !parameters.IsEnabled {
		if err := bipa.DeleteFromServer(ctx, kubeClient); err != nil {
			return fmt.Errorf(baseErrorMessage+
				" - failed to bring the BilinearPodAutoscaler on the server to a fully disabled state. "+
				"The error message reported by the underlying operation follows: %w",
				err)
		}
		return nil
	}

	if err := bipa.reconcileHPA(ctx, kubeClient, parameters.MinReplicaCount, parameters.MaxReplicaCount); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to reconcile the HPA which is part of the BilinearPodAutoscaler on the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	if err := bipa.reconcileVPA(ctx, kubeClient, parameters.ContainerNameApiserver); err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to reconcile the VPA which is part of the BilinearPodAutoscaler on the server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}
	/*
		// Create shoot access token for metrics scraping by gardener-custom-metrics
		shootAccessSecret := bipa.makeShootAccessSecret()
		if err := shootAccessSecret.Reconcile(ctx, kubeClient); err != nil {
			return fmt.Errorf(baseErrorMessage+
				" - failed to create the shoot access token secret '%s' on the server. "+
				"That secret is needed by the gardener-custom-metrics component in order to scrape metrics from the "+
				"shoot's kube-apiserver. "+
				"The error message reported by the underlying operation follows: %w",
				shootAccessSecret.Secret.Name,
				err)
		}

		if err := bipa.reconcileAppResources(ctx, shootAccessSecret.ServiceAccountName, kubeClient); err != nil {
			return err
		}
	*/
	return nil
}

//#region Private implementation

// Returns the name of BilinearPodAutoscaler's server-side HPA
func (bipa *BilinearPodAutoscaler) GetHPAName() string {
	return bipa.deploymentNameApiserver + "-bipa"
}

// Returns the name of BilinearPodAutoscaler's server-side VPA
func (bipa *BilinearPodAutoscaler) GetVPAName() string {
	return bipa.GetHPAName() // We use the same name for the VPA and the HPA objects
}

// Returns an empty HPA object pointing to the server-side HPA, which is part of this BilinearPodAutoscaler
func (bipa *BilinearPodAutoscaler) makeHPA() *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: bipa.GetHPAName(), Namespace: bipa.namespaceName},
	}
}

// Returns an empty VPA object pointing to the server-side VPA, which is part of this BilinearPodAutoscaler
func (bipa *BilinearPodAutoscaler) makeVPA() *vpaautoscalingv1.VerticalPodAutoscaler {
	return &vpaautoscalingv1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: bipa.GetVPAName(), Namespace: bipa.namespaceName},
	}
}

// Reconciles the HPA resource which is part of the BilinearPodAutoscaler.
// minReplicaCount and maxReplicaCount control the horizontal scaling range.
func (bipa *BilinearPodAutoscaler) reconcileHPA(
	ctx context.Context, kubeClient client.Client, minReplicaCount int32, maxReplicaCount int32) error {

	hpa := bipa.makeHPA()
	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, kubeClient, hpa, func() error {
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
					Metric: autoscalingv2.MetricIdentifier{Name: "apiserver_request_total"},
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
			bipa.namespaceName,
			err)
	}

	return nil
}

// Reconciles the VPA resource which is part of the BilinearPodAutoscaler
func (bipa *BilinearPodAutoscaler) reconcileVPA(
	ctx context.Context, kubeClient client.Client, containerNameApiserver string) error {

	vpa := bipa.makeVPA()
	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, kubeClient, vpa, func() error {
		vpa.Spec.Recommenders = nil
		vpa.Spec.TargetRef = &autoscalingv1.CrossVersionObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
			Name:       bipa.deploymentNameApiserver,
		}
		updateModeAutoAsLvalue := vpaautoscalingv1.UpdateModeAuto
		vpa.Spec.UpdatePolicy = &vpaautoscalingv1.PodUpdatePolicy{
			MinReplicas: pointer.Int32(2),
			UpdateMode:  &updateModeAutoAsLvalue,
		}
		vpa.Spec.ResourcePolicy = &vpaautoscalingv1.PodResourcePolicy{
			ContainerPolicies: getVPAContainerResourcePolicies(containerNameApiserver),
		}
		vpa.ObjectMeta.Labels = map[string]string{v1beta1constants.LabelRole: v1beta1constants.LabelAPIServer + "-vpa"}

		return nil
	})

	if err != nil {
		return fmt.Errorf("An error occurred while reconciling the '%s' VPA which is part of the BilinearPodAutoscaler "+
			"in namespace '%s' - failed to apply the desired configuration values to the server-side object. "+
			"The error message reported by the underlying operation follows: %w",
			bipa.GetVPAName(),
			bipa.namespaceName,
			err)
	}

	return nil
}

func getVPAContainerResourcePolicies(containerNameApiserver string) []vpaautoscalingv1.ContainerResourcePolicy {
	scalingModeAutoAsLvalue := vpaautoscalingv1.ContainerScalingModeAuto
	controlledValuesRequestsOnlyAsLvalue := vpaautoscalingv1.ContainerControlledValuesRequestsOnly

	return []vpaautoscalingv1.ContainerResourcePolicy{
		{
			ContainerName: containerNameApiserver,
			Mode:          &scalingModeAutoAsLvalue,
			MinAllowed: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("300m"), // TODO: Andrey: P1: In light of recent experience with removing MinAllowed, do we still want it here?
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

func (bipa *BilinearPodAutoscaler) makeShootAccessSecret() *gardenerutils.AccessSecret {
	return gardenerutils.NewShootAccessSecret(gardenercustommetrics.ComponentName, bipa.namespaceName)
}

// reconcileAppResources reconciles the resources which belong inside the shoot cluster
func (bipa *BilinearPodAutoscaler) reconcileAppResources(ctx context.Context, serviceAccountName string, kubeClient client.Client) error {
	var (
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
				{
					NonResourceURLs: []string{"/delme"},
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
		return err
	}

	return managedresources.CreateForShoot(
		ctx, kubeClient, bipa.namespaceName, gardenercustommetrics.ComponentName, managedresources.LabelValueGardener, false, data)
}

//#endregion Private implementation
