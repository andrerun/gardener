// package kubeobjects defines the k8s objects necessary to materialise the GCMx component on the server side
package kubeobjects

import (
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
)

// GetKubeObjectsAsYamlBytes returns the YAML definitions for all k8s objects necessary to materialise the GCMx component.
// In the resulting map, each object is placed under a key which represents its identity in a format appropriate for use
// as key in map-structured k8s objects, such as Secrets and ConfigMaps.
func GetKubeObjectsAsYamlBytes(deploymentName, namespace, containerImageName, serverSecretName string) (map[string][]byte, error) {
	registry := managedresources.NewRegistry(kubernetes.ShootScheme, kubernetes.ShootCodec, kubernetes.ShootSerializer)

	return registry.AddAllAndSerialize(
		makeServiceAccount(namespace),
		makeEndpointEditorRole(namespace),
		makeEndpointEditorRoleBinding(namespace),
		makeShootReaderClusterRole(),
		makeShootReaderClusterRoleBinding(namespace),
		makeLeaderElectorRole(namespace),
		makeLeaderElectorRoleBinding(namespace),
		makeAuthDelegatorClusterRoleBinding(namespace),
		makeAuthReaderRoleBinding(namespace),
		makeShootVpnAccessNetworkPolicy(namespace),
		makeDeployment(deploymentName, namespace, containerImageName, serverSecretName),
		makeService(namespace),
		makeAPIService(namespace),
		makePDB(namespace),
		makeVPA(namespace),
	)
}
