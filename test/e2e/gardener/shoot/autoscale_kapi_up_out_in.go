package shoot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	e2e "github.com/gardener/gardener/test/e2e/gardener"
	astest "github.com/gardener/gardener/test/e2e/gardener/shoot/internal/autoscaling"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Shoot Tests - Autoscaling", Label("Shoot", "default"), func() {
	isTestMessageImmediate := strings.ToLower(os.Getenv("GARDENER_TEST_MESSAGE_MODE")) == "immediate"
	// A replacement for ginkgo's "By", which, if the environment variable GARDENER_TEST_MESSAGE_MODE is set to
	// "immediate", writes the message immediately to stdout. Used to provide immediate feedback about the progress of
	// long-running tests.
	ByX := func(text string, callback ...func()) {
		if isTestMessageImmediate {
			fmt.Println(text)
		}
		By(text, callback...)
	}

	test := func(shoot *gardencorev1beta1.Shoot) {
		f := defaultShootCreationFramework()
		f.Shoot = shoot

		It("Create shoot, autoscale up, then out, then in", Label("Shoot", "kapi-autoscaling"), func() {
			ctx, cancel := context.WithTimeout(parentCtx, 120*time.Minute)
			defer cancel()

			ByX("creating shoot")
			Expect(f.CreateShootAndWaitForCreation(ctx, false)).To(Succeed())
			defer func() {
				ByX("deleting shoot")
				ctx, cancel = context.WithTimeout(parentCtx, 30*time.Minute)
				defer cancel()
				Expect(f.DeleteShootAndWaitForDeletion(ctx, f.Shoot)).To(Succeed())
			}()
			f.Verify()

			ByX("waiting for HPA and VPA to act on idle shoot kube-apiserver and shrink it to minimal size")
			astest.WaitForIdleKapiState(ctx, f, 60*time.Minute)

			ByX("adding moderate kube-apiserver load to trigger only vertical scaling")
			loader := astest.NewKapiLoader(f.ShootFramework.ShootClient)
			defer loader.SetLoad(0)
			loader.SetLoad(270) // Stay under 300, which is the h-scaling threshold
			astest.WaitForVerticallyInflatedKapiExpectSingleReplica(ctx, f, 50*time.Minute)

			ByX("adding major kube-apiserver load to trigger horizontal scaling")
			loader.SetLoad(330)
			astest.WaitForHorizontallyInflatedKapi(ctx, f, 4*time.Minute)
			Expect(len(astest.GetShootKapiPods(ctx, f))).To(Equal(2))

			ByX("removing all kube-apiserver load to allow HPA to scale back in")
			loader.SetLoad(0)
			astest.WaitForHorizontallyDeflatedKapi(ctx, f, 20*time.Minute)
		})
	}

	Context("Shoot", func() {
		test(e2e.DefaultShoot("e2e-kapiscale"))
	})
})
