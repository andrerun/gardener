package kubeobjects

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

func makePDB(namespace string) *policyv1.PodDisruptionBudget {
	labels := map[string]string{
		"gardener.cloud/role":                 "gardener-custom-metrics",
		"resources.gardener.cloud/managed-by": "gardener",
	}

	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app":                 "gardener-custom-metrics",
			"gardener.cloud/role": "gardener-custom-metrics",
		},
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gardener-custom-metrics",
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable:             &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			UnhealthyPodEvictionPolicy: (*policyv1.UnhealthyPodEvictionPolicyType)(pointer.String(string(policyv1.AlwaysAllow))),
			Selector:                   selector,
		},
	}

	return pdb
}
