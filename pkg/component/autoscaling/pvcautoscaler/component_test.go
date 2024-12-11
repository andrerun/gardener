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
	"context"
	"github.com/Masterminds/semver/v3"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/gardener/gardener/pkg/component/observability/monitoring/prometheus/aggregate"
	monitoringutils "github.com/gardener/gardener/pkg/component/observability/monitoring/utils"
	"github.com/gardener/gardener/pkg/utils/retry"
	retryfake "github.com/gardener/gardener/pkg/utils/retry/fake"
	"github.com/gardener/gardener/pkg/utils/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/component"
	"github.com/gardener/gardener/pkg/resourcemanager/controller/garbagecollector/references"
	secretsutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
	fakesecretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager/fake"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"
)

var _ = Describe("pvcAutoscaler", func() {
	const (
		managedResourceName = "pvc-autoscaler"

		namespace = "some-namespace"
		image     = "some-image:some-tag"
	)

	var (
		ctx       = context.Background()
		c         client.Client
		sm        secretsmanager.Interface
		component component.DeployWaiter
		consistOf func(object ...client.Object) types.GomegaMatcher

		managedResource       *resourcesv1alpha1.ManagedResource
		managedResourceSecret *corev1.Secret

		serviceAccount               *corev1.ServiceAccount
		leaderElectorRole            *rbacv1.Role
		leaderElectorRoleBinding     *rbacv1.RoleBinding
		controllerClusterRole        *rbacv1.ClusterRole
		controllerClusterRoleBinding *rbacv1.ClusterRoleBinding
		proxyClusterRole             *rbacv1.ClusterRole
		proxyClusterRoleBinding      *rbacv1.ClusterRoleBinding
		service                      *corev1.Service
		serviceMonitor               *monitoringv1.ServiceMonitor
		podDisruptionBudgetFor       func(bool) *policyv1.PodDisruptionBudget
		vpa                          *vpaautoscalingv1.VerticalPodAutoscaler

		deploymentFor = func(isUsingAuthorizedMetrics bool) *appsv1.Deployment {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pvc-autoscaler",
					Namespace: namespace,
					Labels: map[string]string{
						"app":                 "pvc-autoscaler",
						"gardener.cloud/role": "pvc-autoscaler",
						"high-availability-config.resources.gardener.cloud/type": "controller",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas:             ptr.To[int32](1),
					RevisionHistoryLimit: ptr.To[int32](2),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app":                 "pvc-autoscaler",
							"gardener.cloud/role": "pvc-autoscaler",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"kubectl.kubernetes.io/default-container": "pvc-autoscaler",
							},
							Labels: map[string]string{
								"app":                              "pvc-autoscaler",
								"gardener.cloud/role":              "pvc-autoscaler",
								"networking.gardener.cloud/to-dns": "allowed",
								"networking.gardener.cloud/to-runtime-apiserver":                   "allowed",
								"networking.resources.gardener.cloud/to-prometheus-cache-tcp-9090": "allowed",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Args: []string{
										"--health-probe-bind-address=:8081",
										"--metrics-bind-address=:8080",
										"--leader-elect",
										"--interval=60s",
										"--prometheus-address=http://prometheus-cache.garden.svc.cluster.local:80",
									},
									Command: []string{"/manager"},
									Image:   image,
									LivenessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path:   "/healthz",
												Scheme: corev1.URISchemeHTTP,
												Port:   intstr.FromInt32(8081),
											},
										},
										InitialDelaySeconds: 20,
										PeriodSeconds:       20,
										TimeoutSeconds:      5,
									},
									Name: "pvc-autoscaler",
									Ports: []corev1.ContainerPort{
										{
											ContainerPort: 8080,
											Name:          "metrics",
											Protocol:      corev1.ProtocolTCP,
										},
									},
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path:   "/readyz",
												Port:   intstr.FromInt32(8081),
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
											corev1.ResourceMemory: resource.MustParse("64Mi"),
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
										"--secure-listen-address=0.0.0.0:8443",
										"--tls-cert-file=/var/run/secrets/gardener.cloud/tls/tls.crt",
										"--tls-private-key-file=/var/run/secrets/gardener.cloud/tls/tls.key",
										"--upstream=http://127.0.0.1:8080/",
										"--logtostderr=true",
										"--v=2",
									},
									Image: "gcr.io/kubebuilder/kube-rbac-proxy:v0.15.0",
									Name:  "kube-rbac-proxy",
									Ports: []corev1.ContainerPort{
										{
											ContainerPort: 8443,
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
											corev1.ResourceMemory: resource.MustParse("64Mi"),
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
											MountPath: "/var/run/secrets/gardener.cloud/tls",
											Name:      "tls",
											ReadOnly:  true,
										},
									},
								},
							},
							PriorityClassName: "gardener-system-700",
							SecurityContext: &corev1.PodSecurityContext{
								RunAsNonRoot: ptr.To(true),
							},
							ServiceAccountName:            "pvc-autoscaler",
							TerminationGracePeriodSeconds: ptr.To(int64(10)),
							Volumes: []corev1.Volume{
								{
									Name: "tls",
									VolumeSource: corev1.VolumeSource{
										Secret: &corev1.SecretVolumeSource{
											DefaultMode: ptr.To(int32(420)),
											SecretName:  "pvc-autoscaler-tls",
										},
									},
								},
							},
						},
					},
				},
			}

			if !isUsingAuthorizedMetrics {
				deployment.Spec.Template.Spec.Containers = deployment.Spec.Template.Spec.Containers[:1]
				deployment.Spec.Template.Spec.Volumes = nil
			}

			return deployment
		}
	)

	BeforeEach(func() {
		const caBundle = "dummy bundle"

		c = fakeclient.NewClientBuilder().WithScheme(kubernetes.SeedScheme).Build()
		sm = fakesecretsmanager.New(c, namespace)

		values := Values{
			Image:             image,
			KubernetesVersion: semver.MustParse("1.26.2"),
		}
		component = New(namespace, values, c, sm)
		consistOf = NewManagedResourceConsistOfObjectsMatcher(c)

		By("Create secrets managed outside of this package for whose secretsmanager.Get() will be called")
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ca-seed", Namespace: namespace},
			Data: map[string][]byte{
				secretsutils.DataKeyCertificateBundle: []byte(caBundle),
			},
		}
		Expect(c.Create(ctx, caSecret)).To(Succeed())

		managedResource = &resourcesv1alpha1.ManagedResource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      managedResourceName,
				Namespace: namespace,
			},
		}
		managedResourceSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "managedresource-" + managedResource.Name,
				Namespace: namespace,
			},
		}

		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pvc-autoscaler",
				Namespace: namespace,
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			AutomountServiceAccountToken: ptr.To(false),
		}
		leaderElectorRole = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gardener.cloud:pvc-autoscaler-leader-elector",
				Namespace: namespace,
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"coordination.k8s.io"},
					Resources: []string{"leases"},
					Verbs:     []string{"create"},
				},
				{
					APIGroups:     []string{"coordination.k8s.io"},
					Resources:     []string{"leases"},
					ResourceNames: []string{"2b09b108.gardener.cloud"},
					Verbs: []string{
						"get",
						"watch",
						"update",
						"delete",
					},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"events"},
					Verbs: []string{
						"create",
						"patch",
					},
				},
			},
		}
		leaderElectorRoleBinding = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gardener.cloud:pvc-autoscaler-leader-elector",
				Namespace: namespace,
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     "gardener.cloud:pvc-autoscaler-leader-elector",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "pvc-autoscaler",
					Namespace: namespace,
				},
			},
		}
		controllerClusterRole = &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gardener.cloud:pvc-autoscaler-controller",
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"events"},
					Verbs:     []string{"create", "patch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"persistentvolumeclaims"},
					Verbs:     []string{"get", "list", "patch", "update", "watch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"persistentvolumeclaims/status"},
					Verbs:     []string{"get"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"storageclasses"},
					Verbs:     []string{"get", "list", "watch"},
				},
			},
		}
		controllerClusterRoleBinding = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gardener.cloud:pvc-autoscaler-controller",
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     "gardener.cloud:pvc-autoscaler-controller",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "pvc-autoscaler",
					Namespace: namespace,
				},
			},
		}
		proxyClusterRole = &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gardener.cloud:pvc-autoscaler-proxy",
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"authentication.k8s.io"},
					Resources: []string{"tokenreviews"},
					Verbs:     []string{"create"},
				},
				{
					APIGroups: []string{"authorization.k8s.io"},
					Resources: []string{"subjectaccessreviews"},
					Verbs:     []string{"create"},
				},
			},
		}
		proxyClusterRoleBinding = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gardener.cloud:pvc-autoscaler-proxy",
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     "gardener.cloud:pvc-autoscaler-proxy",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "pvc-autoscaler",
					Namespace: namespace,
				},
			},
		}
		service = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pvc-autoscaler",
				Namespace: namespace,
				Annotations: map[string]string{
					"networking.resources.gardener.cloud/from-all-seed-scrape-targets-allowed-ports": `[{"protocol":"TCP","port":8080}]`,
				},
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Name:       "secure-metrics",
						Port:       8443,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromString("secure-metrics"),
					},
					{
						Name:       "metrics",
						Port:       8080,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromString("metrics"),
					},
				},
				Selector: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
		}
		serviceMonitor = &monitoringv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "aggregate-pvc-autoscaler",
				Namespace: namespace,
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
					"prometheus":          aggregate.Label,
				},
			},
			Spec: monitoringv1.ServiceMonitorSpec{
				Endpoints: []monitoringv1.Endpoint{
					{
						Port:   "metrics",
						Scheme: "http",
						MetricRelabelConfigs: monitoringutils.StandardMetricRelabelConfig(
							"pvc_autoscaler_max_capacity_reached_total",
							"pvc_autoscaler_resized_total",
							"pvc_autoscaler_skipped_total",
							"pvc_autoscaler_threshold_reached_total",
						),
					},
				},
				NamespaceSelector: monitoringv1.NamespaceSelector{
					MatchNames: []string{namespace},
				},
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				}},
			},
		}
		vpa = &vpaautoscalingv1.VerticalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pvc-autoscaler",
				Namespace: namespace,
				Labels: map[string]string{
					"app":                 "pvc-autoscaler",
					"gardener.cloud/role": "pvc-autoscaler",
				},
			},
			Spec: vpaautoscalingv1.VerticalPodAutoscalerSpec{
				TargetRef: &autoscalingv1.CrossVersionObjectReference{
					APIVersion: appsv1.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       "pvc-autoscaler",
				},
				ResourcePolicy: &vpaautoscalingv1.PodResourcePolicy{
					ContainerPolicies: []vpaautoscalingv1.ContainerResourcePolicy{
						{
							ContainerName:    "*",
							ControlledValues: ptr.To(vpaautoscalingv1.ContainerControlledValuesRequestsOnly),
						},
						{
							ContainerName: "pvc-autoscaler",
							MinAllowed: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("10Mi"),
							},
						},
						{
							ContainerName: "kube-rbac-proxy",
							MinAllowed: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("10Mi"),
							},
						},
					},
				},
			},
		}
		podDisruptionBudgetFor = func(k8sVersionGreaterEquals126 bool) *policyv1.PodDisruptionBudget {
			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pvc-autoscaler",
					Namespace: namespace,
					Labels: map[string]string{
						"app":                 "pvc-autoscaler",
						"gardener.cloud/role": "pvc-autoscaler",
					},
				},
				Spec: policyv1.PodDisruptionBudgetSpec{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app":                 "pvc-autoscaler",
							"gardener.cloud/role": "pvc-autoscaler",
						},
					},
				},
			}

			if k8sVersionGreaterEquals126 {
				pdb.Spec.UnhealthyPodEvictionPolicy = ptr.To(policyv1.AlwaysAllow)
			}

			return pdb
		}
	})

	Describe("#Deploy", func() {
		var expectedObjects []client.Object

		JustBeforeEach(func() {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(managedResource), managedResource)).To(BeNotFoundError())

			Expect(component.Deploy(ctx)).To(Succeed())

			Expect(c.Get(ctx, client.ObjectKeyFromObject(managedResource), managedResource)).To(Succeed())
			expectedMr := &resourcesv1alpha1.ManagedResource{
				ObjectMeta: metav1.ObjectMeta{
					Name:            managedResourceName,
					Namespace:       namespace,
					Labels:          map[string]string{"gardener.cloud/role": "seed-system-component"},
					ResourceVersion: "1",
				},
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: ptr.To("seed"),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: managedResource.Spec.SecretRefs[0].Name,
					}},
					KeepObjects: ptr.To(false),
				},
			}
			utilruntime.Must(references.InjectAnnotations(expectedMr))
			Expect(managedResource).To(DeepEqual(expectedMr))
			expectedObjects = []client.Object{
				serviceAccount,
				leaderElectorRole,
				leaderElectorRoleBinding,
				controllerClusterRole,
				controllerClusterRoleBinding,
				proxyClusterRole,
				proxyClusterRoleBinding,
				deploymentFor(false),
				service,
				serviceMonitor,
				vpa,
			}

			managedResourceSecret.Name = managedResource.Spec.SecretRefs[0].Name
			Expect(c.Get(ctx, client.ObjectKeyFromObject(managedResourceSecret), managedResourceSecret)).To(Succeed())
			Expect(managedResourceSecret.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(managedResourceSecret.Immutable).To(Equal(ptr.To(true)))
			Expect(managedResourceSecret.Labels["resources.gardener.cloud/garbage-collectable-reference"]).To(Equal("true"))

			values := Values{
				Image:             image,
				KubernetesVersion: semver.MustParse("1.26.2"),
			}
			component = New(namespace, values, c, sm)
		})

		It("should successfully deploy all resources", func() {
			expectedObjects = append(expectedObjects, podDisruptionBudgetFor(true))
			Expect(managedResource).To(consistOf(expectedObjects...))
		})
	})

	Describe("#Destroy", func() {
		It("should successfully destroy all resources", func() {
			Expect(c.Create(ctx, managedResource)).To(Succeed())
			Expect(c.Create(ctx, managedResourceSecret)).To(Succeed())

			Expect(component.Destroy(ctx)).To(Succeed())

			Expect(c.Get(ctx, client.ObjectKeyFromObject(managedResource), managedResource)).To(BeNotFoundError())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(managedResourceSecret), managedResourceSecret)).To(BeNotFoundError())
		})
	})

	Context("waiting functions", func() {
		var (
			fakeOps   *retryfake.Ops
			resetVars func()
		)

		BeforeEach(func() {
			fakeOps = &retryfake.Ops{MaxAttempts: 1}
			resetVars = test.WithVars(
				&retry.Until, fakeOps.Until,
				&retry.UntilTimeout, fakeOps.UntilTimeout,
			)
		})

		AfterEach(func() {
			resetVars()
		})

		Describe("#Wait", func() {
			It("should fail when the ManagedResource is missing", func() {
				Expect(component.Wait(ctx)).To(MatchError(ContainSubstring("not found")))
			})

			It("should fail because the ManagedResource doesn't become healthy", func() {
				fakeOps.MaxAttempts = 2

				Expect(c.Create(context.Background(), &resourcesv1alpha1.ManagedResource{
					ObjectMeta: metav1.ObjectMeta{
						Name:       managedResourceName,
						Namespace:  namespace,
						Generation: 1,
					},
					Status: resourcesv1alpha1.ManagedResourceStatus{
						ObservedGeneration: 1,
						Conditions: []gardencorev1beta1.Condition{
							{
								Type:   resourcesv1alpha1.ResourcesApplied,
								Status: gardencorev1beta1.ConditionFalse,
							},
							{
								Type:   resourcesv1alpha1.ResourcesHealthy,
								Status: gardencorev1beta1.ConditionFalse,
							},
						},
					},
				})).To(Succeed())

				Expect(component.Wait(context.Background())).To(MatchError(ContainSubstring("is not healthy")))
			})

			It("should successfully wait for the managed resource to become healthy", func() {
				fakeOps.MaxAttempts = 2

				Expect(c.Create(context.Background(), &resourcesv1alpha1.ManagedResource{
					ObjectMeta: metav1.ObjectMeta{
						Name:       managedResourceName,
						Namespace:  namespace,
						Generation: 1,
					},
					Status: resourcesv1alpha1.ManagedResourceStatus{
						ObservedGeneration: 1,
						Conditions: []gardencorev1beta1.Condition{
							{
								Type:   resourcesv1alpha1.ResourcesApplied,
								Status: gardencorev1beta1.ConditionTrue,
							},
							{
								Type:   resourcesv1alpha1.ResourcesHealthy,
								Status: gardencorev1beta1.ConditionTrue,
							},
						},
					},
				})).To(Succeed())

				Expect(component.Wait(context.Background())).To(Succeed())
			})
		})

		Describe("WaitCleanup()", func() {
			It("should fail when the wait for the managed resource deletion times out", func() {
				fakeOps.MaxAttempts = 2

				managedResource := &resourcesv1alpha1.ManagedResource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      managedResourceName,
						Namespace: namespace,
					},
				}

				Expect(c.Create(ctx, managedResource)).To(Succeed())

				Expect(component.WaitCleanup(ctx)).To(MatchError(ContainSubstring("still exists")))
			})

			It("should not return an error when it's already removed", func() {
				Expect(component.WaitCleanup(ctx)).To(Succeed())
			})
		})
	})
})
