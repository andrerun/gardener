// package autoscaling implements miscellaneous facilities, necessary for the e2e testing of autoscaling functionality
package autoscaling

import (
	"context"
	"fmt"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclientset "k8s.io/client-go/kubernetes"
	"math"
	"sync"
	"time"
)

// KapiLoader loads a K8s cluster with API requests
type KapiLoader struct {
	clientSet             kubernetesclientset.Interface
	requestsPerSecond     int        // current RPS
	loaderProcControlChan chan int   // requestsPerSecond comes through here
	lock                  sync.Mutex // Syncs requestsPerSecond with the commands sent over loaderProcControlChan
}

// NewKapiLoader creates a new KapiLoader which is inactive until a non-zero load is set via SetLoad()
func NewKapiLoader(k8s kubernetes.Interface) *KapiLoader {
	return &KapiLoader{
		clientSet:             k8s.Kubernetes(),
		loaderProcControlChan: make(chan int),
	}
}

// SetLoad sets the load for the cluster. The load remains until a further change is requested.
// The operation is idempotent.
//
// Passing zero stops the load and releases all associated resources. If you set a non-zero load, you must later set
// zero before you can abandon the KapiLoader object, or resources, including active goroutines, may leak.
func (ldr *KapiLoader) SetLoad(requestsPerSecond int) {
	if requestsPerSecond < 0 {
		requestsPerSecond = 0
	}

	ldr.lock.Lock()
	defer ldr.lock.Unlock()

	if ldr.requestsPerSecond == 0 {
		// If oldRps is zero, there is no loader proc running
		if requestsPerSecond == 0 {
			// The command is noop, but take care we don't block sending it to a loader proc that's not there
			return
		} else {
			// Start loader proc
			go loaderProc(ldr.clientSet, ldr.loaderProcControlChan)
		}
	}
	ldr.requestsPerSecond = requestsPerSecond
	ldr.loaderProcControlChan <- requestsPerSecond // Block until command picked by loader proc
}

// makeRequest makes a single article of server load by sending a synchronous Kapi request
func makeRequest(ctx context.Context, clientSet kubernetesclientset.Interface) {
	_, err := clientSet.CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		fmt.Printf("KapiLoader: error making request to server: %s\n", err.Error())
	}
}

// loaderProc blocks until a zero is sent over rpsChan. It also maintains continuous server load of request rate equal
// to the last value sent over rpsChan. The initial rate (before the first value is sent over rpsChan) is zero.
func loaderProc(clientSet kubernetesclientset.Interface, rpsChan <-chan int) {
	rps := <-rpsChan            // Block for initial command
	startTime := time.Now()     // Counts since last command
	var requestsSoFar int64 = 0 // Counts since last command
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for rps != 0 {
		millisecondsSoFar := time.Now().Sub(startTime).Milliseconds()
		desiredRequestsSoFar := millisecondsSoFar * int64(rps) / 1000
		backlog := desiredRequestsSoFar - requestsSoFar
		throttledBacklog := int(math.Min(float64(backlog), 100))
		for i := 0; i < throttledBacklog; i++ {
			go makeRequest(ctx, clientSet)
			requestsSoFar++
		}
		time.Sleep(10 * time.Millisecond)

		select {
		case rps = <-rpsChan:
			startTime = time.Now()
			requestsSoFar = 0
		default:
		}
	}
}
