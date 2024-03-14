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
				"high-availability-config.resources.gardener.cloud/type": "controller",
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
						"app":                                 "gardener-custom-metrics",
						"gardener.cloud/role":                 "gardener-custom-metrics",
						"networking.gardener.cloud/from-seed": "allowed",
						"networking.gardener.cloud/to-dns":    "allowed",
						"networking.gardener.cloud/to-runtime-apiserver":                           "allowed",
						"networking.resources.gardener.cloud/to-all-shoots-kube-apiserver-tcp-443": "allowed",
						"networking.gardener.cloud/to-apiserver":                                   "allowed",
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
									corev1.ResourceMemory: resource.MustParse("800Mi"),
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
								{
									MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
									Name:      "kube-api-access-gardener",
									ReadOnly:  true,
								},
							},
						},
					},
					DNSPolicy:         corev1.DNSClusterFirst,
					PriorityClassName: "gardener-system-700",
					RestartPolicy:     corev1.RestartPolicyAlways,
					SchedulerName:     "default-scheduler",
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{Type: "RuntimeDefault"},
					},
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
						{
							Name: "kube-api-access-gardener",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									DefaultMode: pointer.Int32(420),
									Sources: []corev1.VolumeProjection{
										{
											ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
												ExpirationSeconds: pointer.Int64(43200),
												Path:              "token",
											},
										},
										{
											ConfigMap: &corev1.ConfigMapProjection{
												Items: []corev1.KeyToPath{
													{
														Key:  "ca.crt",
														Path: "ca.crt",
													},
												},
												LocalObjectReference: corev1.LocalObjectReference{
													Name: "kube-root-ca.crt",
												},
											},
										},
										{
											DownwardAPI: &corev1.DownwardAPIProjection{
												Items: []corev1.DownwardAPIVolumeFile{
													{
														FieldRef: &corev1.ObjectFieldSelector{
															APIVersion: "v1",
															FieldPath:  "metadata.namespace",
														},
														Path: "namespace",
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
