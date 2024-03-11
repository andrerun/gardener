// package autoscaling provides utilities which support e2e testing of autoscaling functionality
package autoscaling

import (
	"context"
	"fmt"
	"github.com/gardener/gardener/test/framework"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"time"
)

var (
	// When CPU scales to or below this level, we consider it scaled down to idle state
	kapiIdleCpuLevel = resource.MustParse("300m")
)

// GetShootKapiPods returns the test shoot's kube-apiserver pods. Pods undergoing deletion are excluded from the result.
func GetShootKapiPods(ctx context.Context, fw *framework.ShootCreationFramework) []corev1.Pod {
	seedClientSet := fw.ShootFramework.SeedClient.Kubernetes()

	pods, err := seedClientSet.CoreV1().Pods(fw.Shoot.Status.TechnicalID).List(ctx, metav1.ListOptions{
		TypeMeta:      metav1.TypeMeta{},
		LabelSelector: "app=kubernetes,gardener.cloud/role=controlplane,role=apiserver",
	})
	Expect(err).NotTo(HaveOccurred())

	var alivePods []corev1.Pod
	for i := range pods.Items {
		if pods.Items[i].DeletionTimestamp == nil {
			alivePods = append(alivePods, pods.Items[i])
		}
	}
	return alivePods
}

// WaitForIdleKapiState blocks until the test shoot's kube-apiserver resource request levels settle at idle level.
// It fails the test if that condition is not reached within the specified timeframe.
//
// Remarks: On a newly created cluster this takes 10-15 minutes.
func WaitForIdleKapiState(ctx context.Context, fw *framework.ShootCreationFramework, timeout time.Duration) {
	startTime := time.Now()
	namespace := fw.Shoot.Status.TechnicalID
	clientSet := fw.ShootFramework.SeedClient.Kubernetes()

	for time.Now().Sub(startTime) < timeout {
		pods := GetShootKapiPods(ctx, fw)
		// Wait for pod count in absence of load to reach its minimum
		if len(pods) == 1 {
			// Wait for unloaded kapi pod to be recommended minAllowed CPU
			isCpuRecommendationAtIdleLevel := getShootKapiRecommendedCpu(ctx, fw).Cmp(kapiIdleCpuLevel) <= 0
			if isCpuRecommendationAtIdleLevel {
				printEventTime("idle kapi state reached", startTime)
				// Evict existing pod to force apply idle recommendation
				err := clientSet.CoreV1().Pods(namespace).Delete(ctx, pods[0].Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())

				time.Sleep(10 * time.Second)
				for len(GetShootKapiPods(ctx, fw)) != 1 {
					time.Sleep(5 * time.Second)
				}

				return
			}
		}

		time.Sleep(10 * time.Second)
	}

	Fail("The test cluster kapi did not scale to the 'minimum requests' state within the permitted timeframe", 1)
}

// WaitForVerticallyInflatedKapiExpectSingleReplica blocks until the cpu requests of the shoot kube-apiserver raise above
// minAllowed. It fails the test if that condition is not reached within the specified timeframe.
// It fails the test if at any time the number of said pods differs from 1 (pods marked for deletion do not count).
//
// Remarks: Upscaling is triggered based on VPA's lowerBound, which by default is based on median load. On a young
// cluster which previously has not seen high load, this should take approximately as long as the cluster's age at the
// time of the call - easily over 30 minutes.
func WaitForVerticallyInflatedKapiExpectSingleReplica(
	ctx context.Context, fw *framework.ShootCreationFramework, timeout time.Duration) {

	startTime := time.Now()
	for time.Now().Sub(startTime) < timeout {
		pods := GetShootKapiPods(ctx, fw)
		Expect(pods).To(HaveLen(1))
		kapiContainer := getKapiContainerPointer(&pods[0].Spec)
		Expect(kapiContainer).NotTo(BeNil())
		actualRequests := kapiContainer.Resources.Requests
		Expect(actualRequests.Cpu()).NotTo(BeNil())
		isCpuRequestIncreased := actualRequests.Cpu().Cmp(kapiIdleCpuLevel) > 0
		if isCpuRequestIncreased {
			printEventTime("kapi scaled up", startTime)
			return
		}

		time.Sleep(10 * time.Second)
	}

	Fail("The test cluster kapi did not scale up as result of moderate load within the permitted timeframe", 1)
}

// WaitForHorizontallyInflatedKapi blocks until the test shoot has more than one kube-apiserver pods. The function
// ignores pods marked for deletion. The function fails the test if:
// - Said pod count increases above 2
// - The condition is not reached within the specified timeframe
func WaitForHorizontallyInflatedKapi(ctx context.Context, fw *framework.ShootCreationFramework, timeout time.Duration) {
	startTime := time.Now()
	for time.Now().Sub(startTime) < timeout {
		pods := GetShootKapiPods(ctx, fw)
		if len(pods) > 1 {
			if len(pods) > 2 {
				Fail("The test cluster kapi scaled to unexpectedly high number of replicas", 1)
			}

			printEventTime("kapi scaled out", startTime)
			return
		}

		time.Sleep(5 * time.Second)
	}

	Fail("The test cluster kapi did not scale out as result of high load within the permitted timeframe", 1)
}

// WaitForHorizontallyDeflatedKapi blocks until the test shoot has only one kube-apiserver pod. The function
// ignores pods marked for deletion. The function fails the test if the condition is not reached within the specified timeframe
//
// Remarks: This takes 16-18 minutes, if HPA uses 15 minutes scale-in stabilisation.
func WaitForHorizontallyDeflatedKapi(ctx context.Context, fw *framework.ShootCreationFramework, timeout time.Duration) {
	startTime := time.Now()
	for time.Now().Sub(startTime) < timeout {
		pods := GetShootKapiPods(ctx, fw)
		if len(pods) == 1 {
			// Expect HPA to respect stabilisation window
			Expect(time.Now().Sub(startTime)).To(BeNumerically(">=", 15*time.Minute))
			printEventTime("kapi scaled in", startTime)
			return
		}

		time.Sleep(10 * time.Second)
	}

	Fail("The test cluster kapi did not scale in as result of return to idle load within the permitted timeframe", 1)
}

// getKapiContainerPointer takes a spec of a kube-apiserver pod, and returns a pointer to the kube-apiserver container
func getKapiContainerPointer(podSpec *corev1.PodSpec) *corev1.Container {
	for i := 0; i < len(podSpec.Containers); i++ {
		container := &podSpec.Containers[i]
		if container.Name == "kube-apiserver" {
			return container
		}
	}

	Fail("Failed to identify the kube-apiserver container in a kube-apiserber pod", 1)
	return nil
}

// printEventTime uses stdio to report that the specified event occurred, and how long it took (now - referenceTime)
func printEventTime(event string, referenceTime time.Time) {
	fmt.Printf("%s in %.1f minutes\n", event, time.Now().Sub(referenceTime).Minutes())
}

// getShootKapiRecommendedCpu returns the CPU amount recommended by VPA for the test shoot's kube-apiserver pods
// Fails the test if the recommendation is missing.
func getShootKapiRecommendedCpu(ctx context.Context, fw *framework.ShootCreationFramework) *resource.Quantity {
	vpa := &vpav1.VerticalPodAutoscaler{}
	err := fw.ShootFramework.SeedClient.Client().
		Get(ctx, types.NamespacedName{Namespace: fw.Shoot.Status.TechnicalID, Name: "kube-apiserver-bipa"}, vpa)
	Expect(err).NotTo(HaveOccurred())
	recommendation := vpa.Status.Recommendation
	Expect(recommendation).NotTo(BeNil())
	for i := range recommendation.ContainerRecommendations {
		cr := &recommendation.ContainerRecommendations[i]
		if cr.ContainerName == "kube-apiserver" {
			kapiCpu := cr.Target.Cpu()
			Expect(kapiCpu).NotTo(BeNil())
			return kapiCpu
		}
	}

	Fail("Failed to identify the kube-apiserver container recommendation in a kube-apiserber VPA", 1)
	return nil
}
