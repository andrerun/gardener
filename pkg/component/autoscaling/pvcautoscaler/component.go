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
	"fmt"
	"time"

	"github.com/Masterminds/semver/v3"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/component"
	kubernetesutils "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	secretsutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
)

type pvcAutoscaler struct {
	namespace string
	values    Values

	client         client.Client
	secretsManager secretsmanager.Interface
}

type Values struct {
	Image             string
	KubernetesVersion *semver.Version
}

func New(
	namespace string,
	values Values,
	runtimeClient client.Client,
	secretsManager secretsmanager.Interface,
) component.DeployWaiter {
	return &pvcAutoscaler{
		namespace:      namespace,
		values:         values,
		client:         runtimeClient,
		secretsManager: secretsManager,
	}
}

// Deploy implements [component.Deployer.Deploy].
func (pva *pvcAutoscaler) Deploy(ctx context.Context) error {
	caSecret, found := pva.secretsManager.Get(v1beta1constants.SecretNameCASeed)
	if !found {
		return fmt.Errorf("secret %q not found", v1beta1constants.SecretNameCASeed)
	}
	caBundle := caSecret.Data[secretsutils.DataKeyCertificateBundle]
	if caBundle == nil {
		return fmt.Errorf("secret %q does not contain a certificate bundle", v1beta1constants.SecretNameCASeed)
	}

	serverCertificateSecret, err := pva.deployServerCertificate(ctx)
	if err != nil {
		return fmt.Errorf("failed to delpoy the pvc-autoscaler server TLS certificate: %w", err)
	}

	registry := managedresources.NewRegistry(kubernetes.SeedScheme, kubernetes.SeedCodec, kubernetes.SeedSerializer)

	resources, err := registry.AddAllAndSerialize(
		pva.serviceAccount(),
		pva.leaderElectorRole(),
		pva.leaderElectorRoleBinding(),
		pva.controllerClusterRole(),
		pva.controllerClusterRoleBinding(),
		pva.proxyClusterRole(),
		pva.proxyClusterRoleBinding(),
		pva.deployment(serverCertificateSecret.Name),
		pva.pdb(),
		pva.service(),
		pva.serviceMonitor(),
		pva.vpa(),
	)
	if err != nil {
		return fmt.Errorf("failed to serialize the Kubernetes objects: %w", err)
	}

	err = managedresources.CreateForSeed(
		ctx,
		pva.client,
		pva.namespace,
		managedResourceName,
		false,
		resources)
	if err != nil {
		return fmt.Errorf("failed to deploy ManagedResource '%s/%s': %w", pva.namespace, managedResourceName, err)
	}

	return nil
}

// Destroy implements [component.Deployer.Destroy].
func (pva *pvcAutoscaler) Destroy(ctx context.Context) error {
	if err := managedresources.DeleteForSeed(ctx, pva.client, pva.namespace, managedResourceName); err != nil {
		return fmt.Errorf("failed to delete ManagedResource '%s/%s': %w", pva.namespace, managedResourceName, err)
	}

	return nil
}

// Wait implements [component.Waiter.Wait].
func (pva *pvcAutoscaler) Wait(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, managedResourceTimeout)
	defer cancel()

	if err := managedresources.WaitUntilHealthy(timeoutCtx, pva.client, pva.namespace, managedResourceName); err != nil {
		return fmt.Errorf("failed to wait until ManagedResource '%s/%s' is healthy: %w", pva.namespace, managedResourceName, err)
	}

	return nil
}

// WaitCleanup implements [component.Waiter.WaitCleanup].
func (pva *pvcAutoscaler) WaitCleanup(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, managedResourceTimeout)
	defer cancel()

	if err := managedresources.WaitUntilDeleted(timeoutCtx, pva.client, pva.namespace, managedResourceName); err != nil {
		return fmt.Errorf("failed to wait until ManagedResource '%s/%s' is deleted: %w", pva.namespace, managedResourceName, err)
	}

	return nil
}

const (
	// managedResourceName is the name of the ManagedResource containing the resource specifications.
	managedResourceName = "pvc-autoscaler"
	// serverCertificateSecretName is the name of the Secret containing pvc-autoscaler's HTTPS serving certificate.
	serverCertificateSecretName = "pvc-autoscaler-tls"
	// managedResourceTimeout is the timeout used while waiting for the ManagedResources to become healthy or deleted.
	managedResourceTimeout = 2 * time.Minute
)

// deployServerCertificate deploys the pvc-autoscaler's server TLS certificate to a secret and returns the name
// of the created secret.
//
// Remarks: This function requires the "ca-seed" secret to be present in the pvcAutoscaler.secretsManager
func (pva *pvcAutoscaler) deployServerCertificate(ctx context.Context) (*corev1.Secret, error) {
	serverCertificateSecret, err := pva.secretsManager.Generate(
		ctx,
		&secretsutils.CertificateSecretConfig{
			Name:                        serverCertificateSecretName,
			CommonName:                  serviceName,
			DNSNames:                    kubernetesutils.DNSNamesForService(serviceName, pva.namespace),
			CertType:                    secretsutils.ServerCert,
			SkipPublishingCACertificate: true,
		},
		secretsmanager.SignedByCA(v1beta1constants.SecretNameCASeed, secretsmanager.UseCurrentCA),
		secretsmanager.Rotate(secretsmanager.InPlace))
	if err != nil {
		return nil, fmt.Errorf("failed to generate TLS certificate: %w", err)
	}

	return serverCertificateSecret, nil
}
