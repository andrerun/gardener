package kubeobjects

import (
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func makeService(namespace string) *corev1.Service {
	//This service intentionally does not contain a pod selector. As a result, KCM does not perform any endpoint management.
	//Endpoint management is instead done by the gardener-custom-metrics leader instance, which ensures a single endpoint,
	//directing all traffic to the leader.
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gardener-custom-metrics",
			Namespace: namespace,
			Annotations: map[string]string{
				resourcesv1alpha1.NetworkingFromWorldToPorts: `[{"protocol":"TCP","port":6443}]`,
			},
		},
		Spec: corev1.ServiceSpec{
			IPFamilies: []corev1.IPFamily{corev1.IPv4Protocol},
			Ports: []corev1.ServicePort{
				{
					Port:       443,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt32(6443),
				},
			},
			PublishNotReadyAddresses: true,
			SessionAffinity:          corev1.ServiceAffinityNone,
			Type:                     corev1.ServiceTypeClusterIP,
		},
	}
}
