// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"context"
	pvaconstants "github.com/gardener/gardener/pkg/component/autoscaling/pvcautoscaler/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
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

// EnableAutoscalingOnExistingPVCs configures autoscaling for the PVCs which meet all the following conditions:
// - are in the specified namespace
// - match the specified label selector
// - do not have autoscaling configuration already applied
func EnableAutoscalingOnExistingPVCs(
	ctx context.Context, c client.Client, namespace string, shouldEnable bool, maxAllowed string, selector map[string]string) error {

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
		if _, ok := pvcList.Items[i].Annotations[pvaconstants.AnnotationIsEnabled]; !ok {
			pvc := pvcList.Items[i].DeepCopy()

			if pvc.Annotations == nil {
				pvc.Annotations = map[string]string{}
			}
			pvc.Annotations[pvaconstants.AnnotationIsEnabled] = strconv.FormatBool(shouldEnable)

			if maxAllowed != "" {
				if _, ok := pvcList.Items[i].Annotations["pvc.autoscaling.gardener.cloud/max-capacity"]; !ok {
					pvc.Annotations["pvc.autoscaling.gardener.cloud/max-capacity"] = maxAllowed
				}
			}

			if err := c.Update(ctx, pvc); err != nil {
				return err
			}
		}
	}

	return nil
}
