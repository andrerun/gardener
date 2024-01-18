package gardenercustommetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/component"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/gardener/gardener/pkg/utils/retry"
	retryfake "github.com/gardener/gardener/pkg/utils/retry/fake"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
	fakesecretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager/fake"
	"github.com/gardener/gardener/pkg/utils/test"
)

//#region Fakes

type testBehaviorCapture struct {
	DeployedResourceConfigs component.ResourceConfigs
}

func (capture *testBehaviorCapture) DeployResourceConfigs(
	ctx context.Context,
	client client.Client,
	namespace string,
	clusterType component.ClusterType,
	managedResourceName string,
	registry *managedresources.Registry,
	allResources component.ResourceConfigs,
) error {
	capture.DeployedResourceConfigs = allResources

	return nil
}

//#endregion Fakes

func convertResourceConfigToJson(config *component.ResourceConfig) (string, error) {
	json, err := json.MarshalIndent(config.Obj.(*unstructured.Unstructured), "", "\t")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("\n%s", strings.TrimSuffix(string(json), "\n")), nil
}

var _ = Describe("GardenerCustomMetrics", func() {
	const (
		caSecretName  = "ca-seed"
		imageName     = "test-image"
		namespaceName = "test-namespace"
	)
	var (
		//#region Helpers
		newGcmx = func(isEnabled bool) (*GardenerCustomMetrics, client.Client, secretsmanager.Interface, *testBehaviorCapture) {
			var seedClient client.Client = fakeclient.NewClientBuilder().WithScheme(kubernetes.SeedScheme).Build()
			var fakeSecretsManager secretsmanager.Interface = fakesecretsmanager.New(seedClient, namespaceName)
			gcmx := NewGardenerCustomMetrics(namespaceName, imageName, isEnabled, seedClient, fakeSecretsManager)
			capture := &testBehaviorCapture{}
			// We isolate the deployment workflow at the DeployResourceConfigs() level, because that point offers a
			// convenient, declarative representation
			gcmx.testIsolation.DeployResourceConfigs = capture.DeployResourceConfigs

			return gcmx, seedClient, fakeSecretsManager, capture
		}

		assertServerCertificateOnServer = func(isExpectedToExist bool, seedClient client.Client) {
			actualServerCertificateSecret := corev1.Secret{}
			err := seedClient.Get(
				context.Background(),
				client.ObjectKey{Namespace: namespaceName, Name: serverCertificateSecretName},
				&actualServerCertificateSecret)

			if isExpectedToExist {
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
			} else {
				ExpectWithOffset(1, err).To(HaveOccurred())
				ExpectWithOffset(1, err.Error()).To(MatchRegexp(".*not.*found.*"))
			}
		}

		assertNoManagedResourceOnServer = func(seedClient client.Client) {
			mr := resourcesv1alpha1.ManagedResource{}
			err := seedClient.Get(
				context.Background(), client.ObjectKey{Namespace: namespaceName, Name: managedResourceName}, &mr)
			ExpectWithOffset(1, err).To(HaveOccurred())
			ExpectWithOffset(1, err.Error()).To(MatchRegexp(".*not.*found.*"))
		}

		createObjectOnSeed = func(obj client.Object, name string, seedClient client.Client) {
			obj.SetNamespace(namespaceName)
			obj.SetName(name)
			ExpectWithOffset(1, seedClient.Create(context.Background(), obj)).To(Succeed())
		}
		//#endregion Helpers
	)

	Describe("Deploy()", func() {
		Context("in enabled state", func() {
			It("should deploy the correct resources to the seed", func() {
				//#region Expected resource config values as bulk JSON
				expectedResourceConfigsAsJson := []string{
					`
{
	"apiVersion": "v1",
	"automountServiceAccountToken": true,
	"kind": "ServiceAccount",
	"metadata": {
		"name": "gardener-custom-metrics",
		"namespace": "test-namespace"
	}
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRole",
	"metadata": {
		"name": "gardener-custom-metrics-endpoint-editor"
	},
	"rules": [
		{
			"apiGroups": [
				""
			],
			"resources": [
				"endpoints"
			],
			"verbs": [
				"*"
			]
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRoleBinding",
	"metadata": {
		"name": "gardener-custom-metrics--endpoint-editor"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "ClusterRole",
		"name": "gardener-custom-metrics-endpoint-editor"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace"
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRole",
	"metadata": {
		"name": "gardener-custom-metrics-custom-metrics-editor"
	},
	"rules": [
		{
			"apiGroups": [
				"custom.metrics.k8s.io"
			],
			"resources": [
				"*"
			],
			"verbs": [
				"*"
			]
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRoleBinding",
	"metadata": {
		"name": "gardener-custom-metrics--custom-metrics-editor"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "ClusterRole",
		"name": "gardener-custom-metrics-custom-metrics-editor"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace"
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRole",
	"metadata": {
		"name": "gardener-custom-metrics-pod-reader"
	},
	"rules": [
		{
			"apiGroups": [
				""
			],
			"resources": [
				"pods"
			],
			"verbs": [
				"get",
				"list",
				"watch"
			]
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRoleBinding",
	"metadata": {
		"name": "gardener-custom-metrics--pod-reader"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "ClusterRole",
		"name": "gardener-custom-metrics-pod-reader"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace"
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRole",
	"metadata": {
		"name": "gardener-custom-metrics-secret-reader"
	},
	"rules": [
		{
			"apiGroups": [
				""
			],
			"resources": [
				"secrets"
			],
			"verbs": [
				"get",
				"list",
				"watch"
			]
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRoleBinding",
	"metadata": {
		"name": "gardener-custom-metrics--secret-reader"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "ClusterRole",
		"name": "gardener-custom-metrics-secret-reader"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace"
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRoleBinding",
	"metadata": {
		"name": "gardener-custom-metrics--system:auth-delegator"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "ClusterRole",
		"name": "system:auth-delegator"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace"
		}
	]
}`,
					`
{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "RoleBinding",
	"metadata": {
		"name": "gardener-custom-metrics--auth-reader",
		"namespace": "kube-system"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "Role",
		"name": "extension-apiserver-authentication-reader"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace"
		}
	]
}`,
					`
{
	"apiVersion": "networking.k8s.io/v1",
	"kind": "NetworkPolicy",
	"metadata": {
		"name": "gardener-custom-metrics--ingress-from-vpn-shoot",
		"namespace": "test-namespace"
	},
	"spec": {
		"ingress": [
			{
				"from": [
					{
						"namespaceSelector": {
							"matchLabels": {
								"kubernetes.io/metadata.name": "kube-system"
							}
						},
						"podSelector": {
							"matchLabels": {
								"app": "vpn-shoot",
								"gardener.cloud/role": "system-component"
							}
						}
					}
				],
				"ports": [
					{
						"port": 6443,
						"protocol": "TCP"
					}
				]
			}
		],
		"podSelector": {
			"matchLabels": {
				"app": "gardener-custom-metrics",
				"gardener.cloud/role": "gardener-custom-metrics"
			}
		},
		"policyTypes": [
			"Ingress"
		]
	}
}`,
					// TODO: Andrey: P1: Enable election, how do we decide on replica count?, remove debug flag, reduce verbosity
					`
{
	"apiVersion": "apps/v1",
	"kind": "Deployment",
	"metadata": {
		"labels": {
			"app": "gardener-custom-metrics"
		},
		"name": "gardener-custom-metrics",
		"namespace": "test-namespace"
	},
	"spec": {
		"replicas": 2,
		"selector": {
			"matchLabels": {
				"app": "gardener-custom-metrics",
				"gardener.cloud/role": "gardener-custom-metrics",
				"resources.gardener.cloud/managed-by": "gardener"
			}
		},
		"template": {
			"metadata": {
				"labels": {
					"app": "gardener-custom-metrics",
					"gardener.cloud/role": "gardener-custom-metrics",
					"networking.gardener.cloud/from-seed": "allowed",
					"networking.gardener.cloud/to-apiserver": "allowed",
					"networking.gardener.cloud/to-dns": "allowed",
					"networking.gardener.cloud/to-runtime-apiserver": "allowed",
					"networking.resources.gardener.cloud/to-all-shoots-kube-apiserver-tcp-443": "allowed",
					"resources.gardener.cloud/managed-by": "gardener"
				}
			},
			"spec": {
				"containers": [
					{
						"command": [
							"./gardener-custom-metrics.exe",
							"--secure-port=6443",
							"--tls-cert-file=/var/run/secrets/gardener.cloud/tls/tls.crt",
							"--tls-private-key-file=/var/run/secrets/gardener.cloud/tls/tls.key",
							"--leader-election=false",
							"--namespace=garden",
							"--access-ip=$(POD_IP)",
							"--access-port=6443",
							"--debug",
							"--log-level=75"
						],
						"env": [
							{
								"name": "POD_IP",
								"valueFrom": {
									"fieldRef": {
										"fieldPath": "status.podIP"
									}
								}
							},
							{
								"name": "LEADER_ELECTION_NAMESPACE",
								"valueFrom": {
									"fieldRef": {
										"fieldPath": "metadata.namespace"
									}
								}
							}
						],
						"image": "test-image",
						"imagePullPolicy": "IfNotPresent",
						"name": "gardener-custom-metrics",
						"ports": [
							{
								"containerPort": 6443,
								"name": "metrics-server",
								"protocol": "TCP"
							}
						],
						"resources": {
							"requests": {
								"cpu": "80m",
								"memory": "800Mi"
							}
						},
						"terminationMessagePath": "/dev/termination-log",
						"terminationMessagePolicy": "File",
						"volumeMounts": [
							{
								"mountPath": "/var/run/secrets/gardener.cloud/tls",
								"name": "gardener-custom-metrics-tls",
								"readOnly": true
							},
							{
								"mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
								"name": "kube-api-access-gardener",
								"readOnly": true
							}
						]
					}
				],
				"dnsPolicy": "ClusterFirst",
				"imagePullSecrets": [
					{
						"name": "gardener-custom-metrics-image-pull-secret"
					}
				],
				"restartPolicy": "Always",
				"schedulerName": "default-scheduler",
				"securityContext": {
					"seccompProfile": {
						"type": "RuntimeDefault"
					}
				},
				"serviceAccountName": "gardener-custom-metrics",
				"terminationGracePeriodSeconds": 30,
				"volumes": [
					{
						"name": "gardener-custom-metrics-tls",
						"secret": {
							"secretName": "gardener-custom-metrics-tls"
						}
					},
					{
						"name": "kube-api-access-gardener",
						"projected": {
							"defaultMode": 420,
							"sources": [
								{
									"serviceAccountToken": {
										"expirationSeconds": 43200,
										"path": "token"
									}
								},
								{
									"configMap": {
										"items": [
											{
												"key": "ca.crt",
												"path": "ca.crt"
											}
										],
										"name": "kube-root-ca.crt"
									}
								},
								{
									"downwardAPI": {
										"items": [
											{
												"fieldRef": {
													"apiVersion": "v1",
													"fieldPath": "metadata.namespace"
												},
												"path": "namespace"
											}
										]
									}
								}
							]
						}
					}
				]
			}
		}
	}
}`,
					`
{
	"apiVersion": "v1",
	"kind": "Service",
	"metadata": {
		"name": "gardener-custom-metrics",
		"namespace": "test-namespace"
	},
	"spec": {
		"internalTrafficPolicy": "Cluster",
		"ipFamilies": [
			"IPv4"
		],
		"ipFamilyPolicy": "SingleStack",
		"ports": [
			{
				"port": 443,
				"protocol": "TCP",
				"targetPort": 6443
			}
		],
		"publishNotReadyAddresses": true,
		"sessionAffinity": "None",
		"type": "ClusterIP"
	}
}`,
					`
{
	"apiVersion": "apiregistration.k8s.io/v1",
	"kind": "APIService",
	"metadata": {
		"name": "v1beta2.custom.metrics.k8s.io"
	},
	"spec": {
		"group": "custom.metrics.k8s.io",
		"groupPriorityMinimum": 100,
		"insecureSkipTLSVerify": true,
		"service": {
			"name": "gardener-custom-metrics",
			"namespace": "test-namespace",
			"port": 443
		},
		"version": "v1beta2",
		"versionPriority": 200
	}
}`,
				}
				//#endregion Expected resource config values as bulk JSON

				// Arrange
				gcmx, seedClient, _, capture := newGcmx(true)
				createObjectOnSeed(&corev1.Secret{}, caSecretName, seedClient)

				// Act
				Expect(gcmx.Deploy(context.Background())).To(Succeed())

				// Assert
				actualServerCertificateSecret := corev1.Secret{}
				Expect(seedClient.Get(
					context.Background(),
					client.ObjectKey{Namespace: namespaceName, Name: serverCertificateSecretName},
					&actualServerCertificateSecret),
				).To(Succeed())

				Expect(capture.DeployedResourceConfigs).To(HaveLen(len(expectedResourceConfigsAsJson)))

				for i := range expectedResourceConfigsAsJson {
					actualJson, err := convertResourceConfigToJson(&capture.DeployedResourceConfigs[i])
					Expect(err).To(Succeed())
					message := fmt.Sprintf(
						"The actual resource config JSON at position %d had unexpected value. Actual:\n%s\n"+
							"Expected:\n%s\n",
						i,
						actualJson,
						expectedResourceConfigsAsJson[i])
					Expect(actualJson).To(Equal(expectedResourceConfigsAsJson[i]), message)
				}

				// Check if the TLS secret was created. The fake secret manager currently does not allow verifying that
				// it was invoked with the expected parameters (even indirectly, as the created secret does not fully
				// reflect the parameters given to the fake secret manager). So, at least check that the secret was
				// created
				assertServerCertificateOnServer(true, seedClient)
			})

			It("should fail if CA certificate is missing on the seed", func() {
				// Arrange
				gcmx, _, _, capture := newGcmx(true)

				// Act
				err := gcmx.Deploy(context.Background())

				// Assert
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(MatchRegexp(".*CA.*certificate.*secret.*"))
				Expect(capture.DeployedResourceConfigs).To(BeNil())
			})
		})

		Context("in disabled state", func() {
			It("should not fail if CA certificate is missing on the seed", func() {
				// Arrange
				gcmx, seedClient, _, _ := newGcmx(false)
				actualServerCertificateSecret := corev1.Secret{}
				err := seedClient.Get(
					context.Background(),
					client.ObjectKey{Namespace: namespaceName, Name: caSecretName},
					&actualServerCertificateSecret)
				Expect(err.Error()).To(MatchRegexp(".*not.*found.*"))

				// Act
				err = gcmx.Deploy(context.Background())

				// Assert
				Expect(err).To(Succeed())
			})

			It("should not deploy any resources to the seed", func() {
				// Arrange
				gcmx, seedClient, _, capture := newGcmx(false)

				// Act
				Expect(gcmx.Deploy(context.Background())).To(Succeed())

				// Assert
				Expect(capture.DeployedResourceConfigs).To(BeNil())
				assertServerCertificateOnServer(false, seedClient)
			})

			It("should destroy the resources on the seed", func() {
				// Arrange
				gcmx, seedClient, _, capture := newGcmx(false)
				createObjectOnSeed(&corev1.Secret{}, serverCertificateSecretName, seedClient)
				createObjectOnSeed(&resourcesv1alpha1.ManagedResource{}, managedResourceName, seedClient)

				// Act
				Expect(gcmx.Deploy(context.Background())).To(Succeed())

				// Assert
				assertNoManagedResourceOnServer(seedClient)
				Expect(capture.DeployedResourceConfigs).To(BeNil())
			})
		})
	})

	Describe("Destroy()", func() {
		Context("in enabled state", func() {
			It("should destroy the resources on the seed", func() {
				// Arrange
				gcmx, seedClient, _, capture := newGcmx(true)
				createObjectOnSeed(&corev1.Secret{}, serverCertificateSecretName, seedClient)
				createObjectOnSeed(&resourcesv1alpha1.ManagedResource{}, managedResourceName, seedClient)

				// Act
				Expect(gcmx.Destroy(context.Background())).To(Succeed())

				// Assert
				assertNoManagedResourceOnServer(seedClient)
				Expect(capture.DeployedResourceConfigs).To(BeNil())
			})

			It("should not fail if resources are missing on the seed", func() {
				// Arrange
				gcmx, _, _, _ := newGcmx(true)

				// Act and assert
				Expect(gcmx.Destroy(context.Background())).To(Succeed())
			})
		})

		Context("in disabled state", func() {
			It("should destroy the resources on the seed", func() {
				// Arrange
				gcmx, seedClient, _, capture := newGcmx(false)
				createObjectOnSeed(&corev1.Secret{}, serverCertificateSecretName, seedClient)
				createObjectOnSeed(&resourcesv1alpha1.ManagedResource{}, managedResourceName, seedClient)

				// Act
				Expect(gcmx.Destroy(context.Background())).To(Succeed())

				// Assert
				assertNoManagedResourceOnServer(seedClient)
				Expect(capture.DeployedResourceConfigs).To(BeNil())
			})
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

		Describe("Wait()", func() {
			It("should fail when the ManagedResource is missing", func() {
				// Arrange
				gcmx, _, _, _ := newGcmx(true)

				// Act
				Expect(gcmx.Wait(context.Background())).To(MatchError(ContainSubstring("not found")))
			})

			It("should fail because the ManagedResource doesn't become healthy", func() {
				// Arrange
				gcmx, seedClient, _, _ := newGcmx(true)
				fakeOps.MaxAttempts = 2

				Expect(seedClient.Create(context.Background(), &resourcesv1alpha1.ManagedResource{
					ObjectMeta: metav1.ObjectMeta{
						Name:       managedResourceName,
						Namespace:  namespaceName,
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

				// Act and assert
				Expect(gcmx.Wait(context.Background())).To(MatchError(ContainSubstring("is not healthy")))
			})

			It("should successfully wait for the managed resource to become healthy", func() {
				// Arrange
				gcmx, seedClient, _, _ := newGcmx(true)
				fakeOps.MaxAttempts = 2

				Expect(seedClient.Create(context.Background(), &resourcesv1alpha1.ManagedResource{
					ObjectMeta: metav1.ObjectMeta{
						Name:       managedResourceName,
						Namespace:  namespaceName,
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

				// Act
				Expect(gcmx.Wait(context.Background())).To(Succeed())
			})
		})

		Describe("WaitCleanup()", func() {
			It("should fail when the wait for the managed resource deletion times out", func() {
				// Arrange
				gcmx, seedClient, _, _ := newGcmx(true)
				createObjectOnSeed(&corev1.Secret{}, serverCertificateSecretName, seedClient)
				createObjectOnSeed(&resourcesv1alpha1.ManagedResource{}, managedResourceName, seedClient)
				fakeOps.MaxAttempts = 2

				// Act
				Expect(gcmx.WaitCleanup(context.Background())).To(MatchError(ContainSubstring("still exists")))
			})

			It("should not return an error when it's already removed", func() {
				// Arrange
				gcmx, _, _, _ := newGcmx(true)
				Expect(gcmx.WaitCleanup(context.Background())).To(Succeed())
			})
		})
	})
})
