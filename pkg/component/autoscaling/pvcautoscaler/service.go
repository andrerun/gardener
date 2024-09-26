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

package pvcautoscaler

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	serviceName           = "pvc-autoscaler"
	healthPort            = 8081
	metricsPort           = 8080
	secureMetricsPort     = 8443
	metricsPortName       = "metrics"
	secureMetricsPortName = "secure-metrics"
)

func (pva *pvcAutoscaler) service() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: pva.namespace,
			Annotations: map[string]string{
				"networking.resources.gardener.cloud/from-all-seed-scrape-targets-allowed-ports": fmt.Sprintf(`[{"protocol":"TCP","port":%d}]`, metricsPort),
			},
			Labels: getLabels(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       secureMetricsPortName,
					Port:       secureMetricsPort,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromString(secureMetricsPortName),
				},
				{
					Name:       metricsPortName,
					Port:       metricsPort,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromString(metricsPortName),
				},
			},
			Selector: getLabels(),
		},
	}
}
