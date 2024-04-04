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

package kubeobjects

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func makeDeployment(deploymentName, namespace, containerImageName, serverSecretName string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "gardener-custom-metrics",
				// The actual availability requirement of gardener-custom-metrics is closer to the "controller"
				// availability level (even less, actually). The value below is set to "server" solely to satisfy
				// the requirement for consistency with existing components.
				"high-availability-config.resources.gardener.cloud/type": "server",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:             pointer.Int32(1),
			RevisionHistoryLimit: pointer.Int32(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":                 "gardener-custom-metrics",
					"gardener.cloud/role": "gardener-custom-metrics",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                              "gardener-custom-metrics",
						"gardener.cloud/role":              "gardener-custom-metrics",
						"networking.gardener.cloud/to-dns": "allowed",
						"networking.gardener.cloud/to-runtime-apiserver":                           "allowed",
						"networking.resources.gardener.cloud/to-all-shoots-kube-apiserver-tcp-443": "allowed",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Command: []string{
								"./gardener-custom-metrics",
								"--secure-port=6443",
								"--tls-cert-file=/var/run/secrets/gardener.cloud/tls/tls.crt",
								"--tls-private-key-file=/var/run/secrets/gardener.cloud/tls/tls.key",
								"--leader-election=true",
								"--namespace=garden",
								"--access-ip=$(POD_IP)",
								"--access-port=6443",
								"--log-level=74",
							},
							Env: []corev1.EnvVar{
								{
									Name: "POD_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
								{
									Name: "LEADER_ELECTION_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							Image:           containerImageName,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Name:            "gardener-custom-metrics",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 6443,
									Name:          "metrics-server",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("80m"),
									corev1.ResourceMemory: resource.MustParse("200Mi"),
								},
							},
							TerminationMessagePath:   "/dev/termination-log",
							TerminationMessagePolicy: corev1.TerminationMessageReadFile,
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/var/run/secrets/gardener.cloud/tls",
									Name:      "gardener-custom-metrics-tls",
									ReadOnly:  true,
								},
							},
						},
					},
					DNSPolicy:                     corev1.DNSClusterFirst,
					PriorityClassName:             "gardener-system-700",
					RestartPolicy:                 corev1.RestartPolicyAlways,
					SchedulerName:                 "default-scheduler",
					ServiceAccountName:            "gardener-custom-metrics",
					TerminationGracePeriodSeconds: pointer.Int64(30),
					Volumes: []corev1.Volume{
						{
							Name: "gardener-custom-metrics-tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: serverSecretName,
								},
							},
						},
					},
				},
			},
		},
	}
}
