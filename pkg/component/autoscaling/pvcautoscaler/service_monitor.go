package pvcautoscaler

import (
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gardener/gardener/pkg/component/observability/monitoring/prometheus/aggregate"
	monitoringutils "github.com/gardener/gardener/pkg/component/observability/monitoring/utils"
	"github.com/gardener/gardener/pkg/utils"
)

func (pva *pvcAutoscaler) serviceMonitor() *monitoringv1.ServiceMonitor {
	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aggregate-pvc-autoscaler",
			Namespace: pva.namespace,
			Labels: utils.MergeStringMaps(getLabels(), map[string]string{
				"prometheus": aggregate.Label,
			}),
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:   metricsPortName,
					Scheme: "http",
					// Andrey: P2: Only needed with HTTPS metrics
					//TLSConfig: &monitoringv1.TLSConfig{
					//	InsecureSkipVerify: true,
					//},
					MetricRelabelConfigs: monitoringutils.StandardMetricRelabelConfig(
						"pvc_autoscaler_max_capacity_reached_total",
						"pvc_autoscaler_resized_total",
						"pvc_autoscaler_skipped_total",
						"pvc_autoscaler_threshold_reached_total",
					),
				},
			},
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{pva.namespace},
			},
			Selector: metav1.LabelSelector{MatchLabels: getLabels()},
		},
	}
}
