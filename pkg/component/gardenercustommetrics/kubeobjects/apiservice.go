package kubeobjects

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/pointer"
)

func makeAPIService(namespace string) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIService",
			APIVersion: "apiregistration.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "v1beta2.custom.metrics.k8s.io",
		},
		Spec: apiregistrationv1.APIServiceSpec{
			Service: &apiregistrationv1.ServiceReference{
				Name:      "gardener-custom-metrics",
				Namespace: namespace,
				Port:      pointer.Int32(443),
			},
			Group:                "custom.metrics.k8s.io",
			Version:              "v1beta2",
			GroupPriorityMinimum: 100,
			VersionPriority:      200,
			// The following enables MITM attack between seed kube-apiserver and GCMx. Not ideal, but it's on par with
			// the metrics-server setup. For more information, see https://github.com/kubernetes-sigs/metrics-server/issues/544
			InsecureSkipTLSVerify: true,
		},
	}
}
