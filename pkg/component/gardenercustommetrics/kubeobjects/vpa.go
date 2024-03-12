package kubeobjects

import (
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
)

func makeVPA(namespace string) *vpaautoscalingv1.VerticalPodAutoscaler {
	requestsOnlyAsLvalue := vpaautoscalingv1.ContainerControlledValuesRequestsOnly
	return &vpaautoscalingv1.VerticalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VerticalPodAutoscaler",
			APIVersion: "autoscaling.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gardener-custom-metrics",
			Namespace: namespace,
			Labels: map[string]string{
				"role": "gardener-custom-metrics-vpa",
			},
		},
		Spec: vpaautoscalingv1.VerticalPodAutoscalerSpec{
			TargetRef: &autoscalingv1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "gardener-custom-metrics",
			},
			ResourcePolicy: &vpaautoscalingv1.PodResourcePolicy{
				ContainerPolicies: []vpaautoscalingv1.ContainerResourcePolicy{
					{
						ContainerName:    "gardener-custom-metrics",
						ControlledValues: &requestsOnlyAsLvalue,
						MinAllowed: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("10Mi"),
						},
					},
				},
			},
		},
	}
}
