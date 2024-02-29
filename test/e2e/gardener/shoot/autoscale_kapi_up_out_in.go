package shoot

import (
	"context"
	"fmt"
	"time"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	e2e "github.com/gardener/gardener/test/e2e/gardener"
	astest "github.com/gardener/gardener/test/e2e/gardener/shoot/internal/autoscaling"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Shoot Tests - Autoscaling", Label("Shoot", "default"), func() {
	test := func(shoot *gardencorev1beta1.Shoot) {
		f := defaultShootCreationFramework()
		// f.GardenerFramework.Config.ExistingShootName = "e2e-kapiscale" // TODO: Andrey: P1: remove
		f.Shoot = shoot

		It("Create shoot, autoscale up, then out, then in", Label("Shoot", "kapi-autoscaling"), func() {
			ctx, cancel := context.WithTimeout(parentCtx, 120*time.Minute)
			defer cancel()

			By("creating shoot")
			Expect(f.CreateShootAndWaitForCreation(ctx, false)).To(Succeed())
			defer func() {
				By("deleting shoot")
				ctx, cancel = context.WithTimeout(parentCtx, 30*time.Minute)
				defer cancel()
				Expect(f.DeleteShootAndWaitForDeletion(ctx, f.Shoot)).To(Succeed())
			}()
			f.Verify()

			By("waiting for HPA and VPA to act on idle shoot kube-apiserver and shrink it to minimal size")
			fmt.Println("# Wait to shrink ################################################################")
			astest.WaitForIdleKapiState(ctx, f, 60*time.Minute)

			By("adding moderate kube-apiserver load to trigger only vertical scaling")
			fmt.Println("# Load ################################################################")
			loader := astest.NewKapiLoader(f.ShootFramework.ShootClient)
			defer loader.SetLoad(0)
			loader.SetLoad(270) // Stay under 300, which is the h-scaling threshold
			fmt.Println("# Wait to scale up ################################################################")
			astest.WaitForVerticallyInflatedKapiExpectSingleReplica(ctx, f, 50*time.Minute)

			By("addingAdd major kube-apiserver load to trigger horizontal scaling")
			fmt.Println("# Load more ################################################################")
			loader.SetLoad(330)
			fmt.Println("# Wait to scale out ################################################################")
			astest.WaitForHorizontallyInflatedKapi(ctx, f, 4*time.Minute)
			Expect(len(astest.GetShootKapiPods(ctx, f))).To(Equal(2))

			By("removing all kube-apiserver load to allow HPA to scale back in")
			loader.SetLoad(0)
			fmt.Println("# Wait to shrink back ################################################################")
			astest.WaitForHorizontallyDeflatedKapi(ctx, f, 20*time.Minute)
		})
	}

	Context("Shoot", func() {
		test(e2e.DefaultShoot("e2e-kapiscale"))
	})
})
