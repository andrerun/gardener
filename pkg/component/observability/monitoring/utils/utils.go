// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConfigObjectMeta returns the object meta for a standard Prometheus config object (e.g., ServiceMonitor).
func ConfigObjectMeta(name, namespace, prometheusName string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      prometheusName + "-" + name,
		Namespace: namespace,
		Labels:    Labels(prometheusName),
	}
}

// StandardMetricRelabelConfig returns the standard relabel config for metrics.
func StandardMetricRelabelConfig(allowedMetrics ...string) []monitoringv1.RelabelConfig {
	return []monitoringv1.RelabelConfig{{
		SourceLabels: []monitoringv1.LabelName{"__name__"},
		Action:       "keep",
		Regex:        `^(` + strings.Join(allowedMetrics, "|") + `)$`,
	}}
}

// Labels returns the labels for the respective prometheus instance.
func Labels(prometheusName string) map[string]string {
	return map[string]string{"prometheus": prometheusName}
}

func EnableAutoscalingOnExistingPVCs(ctx context.Context, c client.Client, namespace string, maxAllowed string, selector map[string]string) error {
	pvcList := &corev1.PersistentVolumeClaimList{}
	err := c.List(ctx, pvcList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(selector),
		Namespace:     namespace,
		Limit:         100,
	})
	if err != nil {
		return err
	}

	for i := range pvcList.Items {
		if _, ok := pvcList.Items[i].Annotations["pvc.autoscaling.gardener.cloud/is-enabled"]; !ok {
			pvc := pvcList.Items[i].DeepCopy()
			pvc.Annotations["pvc.autoscaling.gardener.cloud/is-enabled"] = "true"

			if maxAllowed != "" {
				if _, ok := pvcList.Items[i].Annotations["pvc.autoscaling.gardener.cloud/max-allowed"]; !ok {
					pvc.Annotations["pvc.autoscaling.gardener.cloud/max-allowed"] = maxAllowed
				}
			}

			if err := c.Update(ctx, pvc); err != nil {
				return err
			}
		}
	}

	return nil
}
