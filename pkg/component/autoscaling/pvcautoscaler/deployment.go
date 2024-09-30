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
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/utils"
	secretsutils "github.com/gardener/gardener/pkg/utils/secrets"
)

const (
	deploymentName   = "pvc-autoscaler"
	pvaContainerName = "pvc-autoscaler"
)

// getLabels returns a set of labels, common to pvc-autoscaler resources.
func getLabels() map[string]string {
	return map[string]string{
		v1beta1constants.LabelApp:   "pvc-autoscaler",
		v1beta1constants.GardenRole: "pvc-autoscaler",
	}
}

func (pva *pvcAutoscaler) deployment(serverSecretName string) *appsv1.Deployment {
	const (
		tlsSecretMountPath  = "/var/run/secrets/gardener.cloud/tls"
		tlsSecretVolumeName = "tls"
	)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: pva.namespace,
			Labels: utils.MergeStringMaps(getLabels(), map[string]string{
				resourcesv1alpha1.HighAvailabilityConfigType: resourcesv1alpha1.HighAvailabilityConfigTypeController,
			}),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:             ptr.To[int32](1),
			RevisionHistoryLimit: ptr.To[int32](2),
			Selector: &metav1.LabelSelector{
				MatchLabels: getLabels(),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kubectl.kubernetes.io/default-container": pvaContainerName,
					},
					Labels: utils.MergeStringMaps(getLabels(), map[string]string{
						v1beta1constants.LabelNetworkPolicyToDNS:                           v1beta1constants.LabelNetworkPolicyAllowed,
						v1beta1constants.LabelNetworkPolicyToRuntimeAPIServer:              v1beta1constants.LabelNetworkPolicyAllowed,
						"networking.resources.gardener.cloud/to-prometheus-cache-tcp-9090": v1beta1constants.LabelNetworkPolicyAllowed,
					}),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Args: []string{
								fmt.Sprintf("--health-probe-bind-address=:%d", healthPort),
								fmt.Sprintf("--metrics-bind-address=:%d", metricsPort),
								"--leader-elect",
								"--interval=60s",
								"--prometheus-address=http://prometheus-cache.garden.svc.cluster.local:80",
								//"--namespace=" + pva.namespace,
							},
							Command: []string{"/manager"},
							Image:   pva.values.Image,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/healthz",
										Scheme: corev1.URISchemeHTTP,
										Port:   intstr.FromInt32(healthPort),
									},
								},
								InitialDelaySeconds: 20,
								PeriodSeconds:       20,
								TimeoutSeconds:      5,
							},
							Name: pvaContainerName,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: metricsPort,
									Name:          "metrics",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/readyz",
										Port:   intstr.FromInt32(healthPort),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("10Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"), // TODO: Andrey: P2: Deploy on Canary and update based on actual usage
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
						{
							Args: []string{
								fmt.Sprintf("--secure-listen-address=0.0.0.0:%d", secureMetricsPort),
								"--tls-cert-file=" + filepath.Join(tlsSecretMountPath, secretsutils.DataKeyCertificate),
								"--tls-private-key-file=" + filepath.Join(tlsSecretMountPath, secretsutils.DataKeyPrivateKey),
								fmt.Sprintf("--upstream=http://127.0.0.1:%d/", metricsPort),
								"--logtostderr=true",
								"--v=2",
							},
							Image: "gcr.io/kubebuilder/kube-rbac-proxy:v0.15.0", // TODO: Andrey: P2: This should be parameterised, but we'll likely dispense with the whole kube-rbac-proxy container, so I'm keeping it hardcoded until deleted.
							Name:  "kube-rbac-proxy",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: secureMetricsPort,
									Name:          "secure-metrics",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("5m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"), // TODO: Andrey: P2: Deploy on Canary and update based on actual usage
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: tlsSecretMountPath,
									Name:      tlsSecretVolumeName,
									ReadOnly:  true,
								},
							},
						},
					},
					PriorityClassName: v1beta1constants.PriorityClassNameSeedSystem700,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					ServiceAccountName:            serviceAccountName,
					TerminationGracePeriodSeconds: ptr.To(int64(10)),
					Volumes: []corev1.Volume{
						{
							Name: tlsSecretVolumeName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									DefaultMode: ptr.To(int32(420)),
									SecretName:  serverSecretName,
								},
							},
						},
					},
				},
			},
		},
	}
}
