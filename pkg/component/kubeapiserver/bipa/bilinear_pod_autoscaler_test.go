package bipa

import (
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils/test/matchers"
)

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BilinearPodAutoscaler", func() {
	const (
		containerNameApiserver = "kube-apiserver"
	)
	var (
		deploymentName = "test-deployment"
		namespaceName  = "test-namespace"
		hpaName        = deploymentName + "-bipa"
		vpaName        = hpaName

		kubeClient client.Client
		ctx        = context.Background()

		//#region Helpers
		assertObjectNotOnServer = func(obj client.Object, name string) {
			err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespaceName, Name: name}, obj)
			ExpectWithOffset(1, err).To(HaveOccurred())
			ExpectWithOffset(1, err).To(matchers.BeNotFoundError())
		}

		newBipa = func(isEnabled bool) (*BilinearPodAutoscaler, *DesiredStateParameters) {
			return NewBilinearPodAutoscaler(namespaceName, deploymentName),
				&DesiredStateParameters{
					IsEnabled:              isEnabled,
					MinReplicaCount:        1,
					MaxReplicaCount:        4,
					ContainerNameApiserver: containerNameApiserver,
				}
		}

		newExpectedHpa = func(minReplicaCount int32, maxReplicaCount int32) *autoscalingv2.HorizontalPodAutoscaler {
			lvalue300 := resource.MustParse("300")
			return &autoscalingv2.HorizontalPodAutoscaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: autoscalingv2.SchemeGroupVersion.String(),
					Kind:       "HorizontalPodAutoscaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            hpaName,
					Namespace:       namespaceName,
					Labels:          map[string]string{v1beta1constants.LabelRole: v1beta1constants.LabelAPIServer + "-hpa"},
					ResourceVersion: "1",
				},
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					MinReplicas: &minReplicaCount,
					MaxReplicas: maxReplicaCount,
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       deploymentName,
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: pointer.Int32(900),
						},
					},
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.PodsMetricSourceType,
							Pods: &autoscalingv2.PodsMetricSource{
								Metric: autoscalingv2.MetricIdentifier{Name: "shoot:apiserver_request_total:sum"},
								Target: autoscalingv2.MetricTarget{AverageValue: &lvalue300, Type: autoscalingv2.AverageValueMetricType},
							},
						},
					},
				},
			}
		}

		scalingModeAutoAsLvalue              = vpaautoscalingv1.ContainerScalingModeAuto
		controlledValuesRequestsOnlyAsLvalue = vpaautoscalingv1.ContainerControlledValuesRequestsOnly

		newExpectedVpa = func() *vpaautoscalingv1.VerticalPodAutoscaler {
			updateModeAutoAsLvalue := vpaautoscalingv1.UpdateModeAuto
			return &vpaautoscalingv1.VerticalPodAutoscaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: vpaautoscalingv1.SchemeGroupVersion.String(),
					Kind:       "VerticalPodAutoscaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            vpaName,
					Namespace:       namespaceName,
					Labels:          map[string]string{v1beta1constants.LabelRole: v1beta1constants.LabelAPIServer + "-vpa"},
					ResourceVersion: "1",
				},
				Spec: vpaautoscalingv1.VerticalPodAutoscalerSpec{
					TargetRef: &autoscalingv1.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       deploymentName,
					},
					UpdatePolicy: &vpaautoscalingv1.PodUpdatePolicy{
						MinReplicas: pointer.Int32(1),
						UpdateMode:  &updateModeAutoAsLvalue,
					},
					ResourcePolicy: &vpaautoscalingv1.PodResourcePolicy{
						ContainerPolicies: []vpaautoscalingv1.ContainerResourcePolicy{
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
						},
					},
				},
			}
		}
		//#endregion Helpers
	)

	BeforeEach(func() {
		kubeClient = fakeclient.NewClientBuilder().WithScheme(kubernetes.SeedScheme).Build()
	})

	Describe(".Reconcile()", func() {
		Context("in enabled state", func() {
			It("should deploy the correct resources to the shoot control plane", func() {
				// Arrange
				bipa, desiredState := newBipa(true)

				// Act
				Expect(bipa.Reconcile(ctx, kubeClient, desiredState)).To(Succeed())

				// Assert
				actualHpa := autoscalingv2.HorizontalPodAutoscaler{}
				Expect(kubeClient.Get(
					ctx,
					client.ObjectKey{Namespace: namespaceName, Name: hpaName},
					&actualHpa),
				).To(Succeed())
				Expect(&actualHpa).To(matchers.DeepEqual(newExpectedHpa(desiredState.MinReplicaCount, desiredState.MaxReplicaCount)))

				actualVpa := vpaautoscalingv1.VerticalPodAutoscaler{}
				Expect(kubeClient.Get(
					ctx,
					client.ObjectKey{Namespace: namespaceName, Name: vpaName},
					&actualVpa),
				).To(Succeed())
				Expect(&actualVpa).To(matchers.DeepEqual(newExpectedVpa()))
			})
		})
		Context("in disabled state", func() {
			It("should not deploy any resources to the shoot control plane", func() {
				// Arrange
				bipa, desiredState := newBipa(false)

				// Act
				Expect(bipa.Reconcile(ctx, kubeClient, desiredState)).To(Succeed())

				// Assert
				assertObjectNotOnServer(&autoscalingv2.HorizontalPodAutoscaler{}, hpaName)
				assertObjectNotOnServer(&vpaautoscalingv1.VerticalPodAutoscaler{}, vpaName)
			})
			It("should remove the respective resources already in the shoot control plane", func() {
				// Arrange
				bipa, desiredState := newBipa(true)
				Expect(bipa.reconcileHPA(ctx, kubeClient, desiredState.MinReplicaCount, desiredState.MaxReplicaCount)).To(Succeed())
				desiredState.IsEnabled = false

				// Act
				Expect(bipa.Reconcile(ctx, kubeClient, desiredState)).To(Succeed())

				// Assert
				assertObjectNotOnServer(&autoscalingv2.HorizontalPodAutoscaler{}, hpaName)
				assertObjectNotOnServer(&vpaautoscalingv1.VerticalPodAutoscaler{}, vpaName)
			})
		})
	})
	Describe(".DeleteFromServer()", func() {
		Context("in enabled state", func() {
			It("should remove the respective resources in the shoot control plane", func() {
				// Arrange
				bipa, desiredState := newBipa(true)
				Expect(bipa.reconcileHPA(ctx, kubeClient, desiredState.MinReplicaCount, desiredState.MaxReplicaCount)).To(Succeed())

				// Act
				Expect(bipa.DeleteFromServer(ctx, kubeClient)).To(Succeed())

				// Assert
				assertObjectNotOnServer(&autoscalingv2.HorizontalPodAutoscaler{}, hpaName)
				assertObjectNotOnServer(&vpaautoscalingv1.VerticalPodAutoscaler{}, vpaName)
			})
			It("should not fail if resources are missing on the seed", func() {
				// Arrange
				bipa, _ := newBipa(true)

				// Act
				err := bipa.DeleteFromServer(ctx, kubeClient)

				// Assert
				Expect(err).To(Succeed())
				assertObjectNotOnServer(&autoscalingv2.HorizontalPodAutoscaler{}, hpaName)
				assertObjectNotOnServer(&vpaautoscalingv1.VerticalPodAutoscaler{}, vpaName)
			})
		})
		Context("in disabled state", func() {
			It("should remove the respective resources in the shoot control plane", func() {
				// Arrange
				bipa, desiredState := newBipa(true)
				Expect(bipa.reconcileHPA(ctx, kubeClient, desiredState.MinReplicaCount, desiredState.MaxReplicaCount)).To(Succeed())
				desiredState.IsEnabled = false

				// Act
				Expect(bipa.DeleteFromServer(ctx, kubeClient)).To(Succeed())

				// Assert
				assertObjectNotOnServer(&autoscalingv2.HorizontalPodAutoscaler{}, hpaName)
				assertObjectNotOnServer(&vpaautoscalingv1.VerticalPodAutoscaler{}, vpaName)
			})
		})
	})
})
