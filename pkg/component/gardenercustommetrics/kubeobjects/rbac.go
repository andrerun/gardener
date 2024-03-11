package kubeobjects

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeEndpointEditorClusterRole() *rbacv1.ClusterRole {
	clusterRole := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--endpoint-editor",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"endpoints"},
				// resourceNames: [ "gardener-custom-metrics" ] // TODO: Andrey: P1: Restrict by name
				Verbs: []string{"*"},
			},
		},
	}

	return clusterRole
}

func makeEndpointEditorClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--endpoint-editor",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "gardener-custom-metrics--endpoint-editor",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "gardener-custom-metrics",
				Namespace: namespace,
			},
		},
	}
}

func makePodReaderClusterRole() *rbacv1.ClusterRole {
	clusterRole := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--pod-reader",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}

	return clusterRole
}

func makePodReaderClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--pod-reader",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "gardener-custom-metrics--pod-reader",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "gardener-custom-metrics",
				Namespace: namespace,
			},
		},
	}
}

func makeSecretReaderClusterRole() *rbacv1.ClusterRole {
	clusterRole := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--secret-reader",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				// resourceNames: [ "ca", "shoot-access-gardener-custom-metrics" ] // TODO: Restrict by name after the necessary controller manager custom cache is implemented
				Verbs: []string{"get", "list", "watch"},
			},
		},
	}

	return clusterRole
}

func makeSecretReaderClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	roleRef := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     "gardener-custom-metrics--secret-reader",
	}

	subject := rbacv1.Subject{
		Kind:      "ServiceAccount",
		Name:      "gardener-custom-metrics",
		Namespace: namespace,
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--secret-reader",
		},
		RoleRef:  roleRef,
		Subjects: []rbacv1.Subject{subject},
	}

	return clusterRoleBinding
}

func makeLeaseEditorRole(namespace string) *rbacv1.Role {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "Role",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gardener-custom-metrics--lease-editor",
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{"leases"},
				Verbs: []string{
					"get",
					"list",
					"watch",
					"create",
					"update",
					"patch",
					"delete",
					"deletecollection",
				},
			},
		},
	}

	return role
}

func makeLeaseEditorRoleBinding(namespace string) *rbacv1.RoleBinding {
	// Create a new RoleBinding object
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gardener-custom-metrics--lease-editor",
			Namespace: namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "gardener-custom-metrics--lease-editor",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "gardener-custom-metrics",
				Namespace: namespace,
			},
		},
	}

	return roleBinding
}

//#region Bindings to externally defined roles

func makeAuthDelegatorClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	roleRef := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     "system:auth-delegator",
	}

	subject := rbacv1.Subject{
		Kind:      "ServiceAccount",
		Name:      "gardener-custom-metrics",
		Namespace: namespace,
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gardener-custom-metrics--system:auth-delegator",
		},
		RoleRef:  roleRef,
		Subjects: []rbacv1.Subject{subject},
	}

	return clusterRoleBinding
}

func makeAuthReaderRoleBinding(namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gardener-custom-metrics--auth-reader",
			Namespace: "kube-system",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "extension-apiserver-authentication-reader",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "gardener-custom-metrics",
				Namespace: namespace,
			},
		},
	}
}

//#endregion Bindings to externally defined roles
