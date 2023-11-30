// Package gardenercustommetrics implements the gardener-custom-metrics seed component.
// For details, see the GardenerCustomMetrics type.
package gardenercustommetrics

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener/pkg/component"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
)

// GardenerCustomMetrics manages an instance of the gardener-custom-metrics component. The component is deployed on a
// seed, scrapes the metrics on all shoot, and provides custom metrics by registering as APIService at the custom
// metrics extension point of the seed kube-apiserver.
type GardenerCustomMetrics struct {
	namespaceName      string
	containerImageName string
	isEnabled          bool

	kubeClient              client.Client
	secretsManager          secretsmanager.Interface
	managedResourceRegistry *managedresources.Registry

	testIsolation gardenerCustomMetricsTestIsolation // Provides indirections necessary to isolate the unit during tests
}

// Creates a new GardenerCustomMetrics instance tied to a specific server connection
//
// namespace is where the server-side artefacts (e.g. pods) will be deployed
// containerImageName points to the binary for the pods
// If enabled is true, this instance strives to bring the component to an installed, working state. If enabled is
// false, this instance strives to uninstall the component.
// kubeClient represents the connection to the seed API server.
// secretsManager is used to interact with secrets on the seed.
func NewGardenerCustomMetrics(
	namespace string,
	containerImageName string,
	enabled bool,
	kubeClient client.Client,
	secretsManager secretsmanager.Interface) *GardenerCustomMetrics {

	return &GardenerCustomMetrics{
		namespaceName:      namespace,
		containerImageName: containerImageName,
		isEnabled:          enabled,
		kubeClient:         kubeClient,
		secretsManager:     secretsManager,
		managedResourceRegistry: managedresources.NewRegistry(
			kubernetes.SeedScheme, kubernetes.SeedCodec, kubernetes.SeedSerializer),

		testIsolation: gardenerCustomMetricsTestIsolation{
			DeployResourceConfigs:  component.DeployResourceConfigs,
			DestroyResourceConfigs: component.DestroyResourceConfigs,
		},
	}
}

// Deploy implements [component.Deployer.Deploy]()
func (gcmx *GardenerCustomMetrics) Deploy(ctx context.Context) error {
	baseErrorMessage :=
		fmt.Sprintf(
			"An error occurred while deploying GardenerCustomMetrics component in namespace '%s' of the seed server",
			gcmx.namespaceName)

	if !gcmx.isEnabled {
		if err := gcmx.Destroy(ctx); err != nil {
			return fmt.Errorf(baseErrorMessage+
				" - failed to bring the GardenerCustomMetrics on the server to a disabled state. "+
				"The error message reported by the underlying operation follows: %w",
				err)
		}
		return nil
	}

	serverCertificateSecret, err := gcmx.deployServerCertificate(ctx)
	if err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to deploy the gardener-custom-metrics server TLS certificate to the seed server. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	resourceConfigs, err := getResourceConfigs(gcmx.namespaceName, gcmx.containerImageName, serverCertificateSecret)
	if err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to acquire the necessary resource config objects, which are to describe the individual "+
			"elements which need to be deployed. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	err = gcmx.testIsolation.DeployResourceConfigs(
		ctx,
		gcmx.kubeClient,
		gcmx.namespaceName,
		component.ClusterTypeSeed,
		managedResourceName,
		gcmx.managedResourceRegistry,
		resourceConfigs)
	if err != nil {
		return fmt.Errorf(baseErrorMessage+
			" - failed to deploy the necessary resource config objects as a ManagedResource named '%s' to the server. "+
			"The error message reported by the underlying operation follows: %w",
			managedResourceName,
			err)
	}

	return nil
}

// Destroy implements [component.Deployer.Destroy]()
func (gcmx *GardenerCustomMetrics) Destroy(ctx context.Context) error {
	if err := gcmx.testIsolation.DestroyResourceConfigs(
		ctx, gcmx.kubeClient, gcmx.namespaceName, component.ClusterTypeSeed, managedResourceName); err != nil {

		return fmt.Errorf(
			"An error occurred while removing the GardenerCustomMetrics component in namespace '%s' from the seed server"+
				" - failed to remove ManagedResource '%s'. "+
				"The error message reported by the underlying operation follows: %w",
			gcmx.namespaceName,
			managedResourceName,
			err)
	}

	return nil
}

// Wait implements [component.Waiter.Wait]()
func (gcmx *GardenerCustomMetrics) Wait(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, managedResourceTimeout)
	defer cancel()

	if err := managedresources.WaitUntilHealthy(timeoutCtx, gcmx.kubeClient, gcmx.namespaceName, managedResourceName); err != nil {
		return fmt.Errorf(
			"An error occurred while waiting for the deployment process of GardenerCustomMetrics component to "+
				"'%s' namespace in the seed server to finish and for the component to report ready status"+
				" - the wait for ManagedResource '%s' to become healty failed. "+
				"The error message reported by the underlying operation follows: %w",
			gcmx.namespaceName,
			managedResourceName,
			err)
	}

	return nil
}

// WaitCleanup implements [component.Waiter.WaitCleanup]()
func (gcmx *GardenerCustomMetrics) WaitCleanup(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, managedResourceTimeout)
	defer cancel()

	if err := managedresources.WaitUntilDeleted(timeoutCtx, gcmx.kubeClient, gcmx.namespaceName, managedResourceName); err != nil {
		return fmt.Errorf(
			"An error occurred while waiting for the GardenerCustomMetrics component to be fully removed from the "+
				"'%s' namespace in the seed server"+
				" - the wait for ManagedResource '%s' to be removed failed. "+
				"The error message reported by the underlying operation follows: %w",
			gcmx.namespaceName,
			managedResourceName,
			err)
	}

	return nil
}

const (
	componentBaseName           = "gardener-custom-metrics"
	deploymentName              = componentBaseName
	managedResourceName         = componentBaseName // The implementing artifacts are deployed to the seed via this MR
	serviceName                 = componentBaseName
	serverCertificateSecretName = componentBaseName + "-tls" // GCMx's HTTPS certificate
	managedResourceTimeout      = 2 * time.Minute            // Timeout for ManagedResources to become healthy or deleted
)

// gardenerCustomMetricsTestIsolation contains all points of indirection necessary to isolate GardenerCustomMetrics'
// dependencies on external static functions, during tests.
type gardenerCustomMetricsTestIsolation struct {
	// Points to [component.DeployResourceConfigs]()
	DeployResourceConfigs func(
		context.Context, client.Client, string, component.ClusterType, string, *managedresources.Registry, component.ResourceConfigs) error

	// Points to [component.DestroyResourceConfigs]()
	DestroyResourceConfigs func(
		context.Context, client.Client, string, component.ClusterType, string, ...component.ResourceConfigs) error
}

// Deploys the GCMx server TLS certificate to a secret and returns the name of the created secret
func (gcmx *GardenerCustomMetrics) deployServerCertificate(ctx context.Context) (*corev1.Secret, error) {
	const baseErrorMessage = "An error occurred while deploying server TLS certificate for gardener-custom-metrics"

	_, found := gcmx.secretsManager.Get(v1beta1constants.SecretNameCASeed)
	if !found {
		return nil, fmt.Errorf(
			baseErrorMessage+
				" - the CA certificate, which is required to sign said server certificate, is missing. "+
				"The CA certificate was expected in the '%s' secret, but that secret was not found",
			v1beta1constants.SecretNameCASeed)
	}

	serverCertificateSecret, err := gcmx.secretsManager.Generate(
		ctx,
		&secretutils.CertificateSecretConfig{
			Name:                        serverCertificateSecretName,
			CommonName:                  fmt.Sprintf("%s.%s.svc", serviceName, gcmx.namespaceName),
			DNSNames:                    kutil.DNSNamesForService(serviceName, gcmx.namespaceName),
			CertType:                    secretutils.ServerCert,
			SkipPublishingCACertificate: true,
		},
		secretsmanager.SignedByCA(v1beta1constants.SecretNameCASeed, secretsmanager.UseCurrentCA),
		secretsmanager.Rotate(secretsmanager.InPlace))
	if err != nil {
		return nil, fmt.Errorf(
			baseErrorMessage+
				" - the attept to generate the certificate and store it in a secret named '%s' failed. "+
				"The error message reported by the underlying operation follows: %w",
			serverCertificateSecretName,
			err)
	}

	return serverCertificateSecret, nil
}

// Returns a list of the seed resources required to support GCMx's operation, in the form of a [component.ResourceConfigs]
func getResourceConfigs(
	namespaceName string, containerImageName string, serverCertificateSecret *corev1.Secret) (component.ResourceConfigs, error) {

	const baseErrorMessage = "An error occurred while retrieving the list of resource config objects describing the " +
		"elements of the GardenerCustomMetrics component"

	manifestReaders, err := getManifests(namespaceName, containerImageName, serverCertificateSecret)
	if err != nil {
		return nil, fmt.Errorf(baseErrorMessage+" - failed to retrieve manifest data. "+
			"The error message reported by the underlying operation follows: %w",
			err)
	}

	var allResources component.ResourceConfigs
	for i, manifest := range manifestReaders {
		manifestObjects, err := readManifest(manifest)
		if err != nil {
			msg := baseErrorMessage + " - failed to parse the manifest at index %d. " +
				"The error message reported by the underlying operation follows: %w"
			return nil, fmt.Errorf(msg, i, err)
		}

		for _, manifestObject := range manifestObjects {
			resourceConfig := component.ResourceConfig{
				Obj:   manifestObject,
				Class: component.Runtime,
			}
			allResources = append(allResources, resourceConfig)
		}
	}

	return allResources, nil
}
