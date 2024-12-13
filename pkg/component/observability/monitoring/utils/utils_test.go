// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package utils_test

import (
	"context"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	pvaconstants "github.com/gardener/gardener/pkg/component/autoscaling/pvcautoscaler/constants"
	monitoringutils "github.com/gardener/gardener/pkg/component/observability/monitoring/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Utils", func() {
	Describe("#ConfigObjectMeta", func() {
		It("should return the expected object meta", func() {
			Expect(monitoringutils.ConfigObjectMeta("foo", "bar", "baz")).To(Equal(metav1.ObjectMeta{
				Name:      "baz-foo",
				Namespace: "bar",
				Labels:    map[string]string{"prometheus": "baz"},
			}))
		})
	})

	Describe("#StandardMetricRelabelConfig", func() {
		It("should return the expected relabel configs", func() {
			Expect(monitoringutils.StandardMetricRelabelConfig("foo", "bar", "baz")).To(HaveExactElements(monitoringv1.RelabelConfig{
				SourceLabels: []monitoringv1.LabelName{"__name__"},
				Action:       "keep",
				Regex:        `^(foo|bar|baz)$`,
			}))
		})
	})

	Describe("#Labels", func() {
		It("should return the expected labels", func() {
			Expect(monitoringutils.Labels("foo")).To(Equal(map[string]string{"prometheus": "foo"}))
		})
	})

	Describe("#EnableAutoscalingOnExistingPVCs", func() {
		const (
			namespace      = "some-namespace"
			pvcName        = "preexisting-pvc"
			pvcName2       = "preexisting-pvc-2"
			pvcLabelKey    = "some-key"
			pvcLabelValue  = "some-value"
			pvcLabelValue2 = "some-value-2"
			maxAllowed     = "777Gi"
		)
		var (
			ctx        context.Context
			fakeClient client.Client
		)

		BeforeEach(func() {
			ctx = context.Background()
			fakeClient = fakeclient.NewClientBuilder().WithScheme(kubernetes.SeedScheme).Build()
		})

		It("should set the requested values on existing PVCs", func() {
			Expect(fakeClient.Create(ctx, &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: namespace,
					Labels: map[string]string{
						pvcLabelKey: pvcLabelValue,
					},
				},
			})).To(Succeed())

			Expect(monitoringutils.EnableAutoscalingOnExistingPVCs(
				ctx, fakeClient, namespace, true, maxAllowed, map[string]string{pvcLabelKey: pvcLabelValue})).
				To(Succeed())

			var actualPvc corev1.PersistentVolumeClaim
			Expect(fakeClient.Get(ctx, client.ObjectKey{namespace, pvcName}, &actualPvc)).To(Succeed())
			Expect(actualPvc.Annotations).NotTo(BeNil())
			Expect(actualPvc.Annotations[pvaconstants.AnnotationIsEnabled]).To(Equal("true"))
			Expect(actualPvc.Annotations).NotTo(HaveKey(pvaconstants.AnnotationMinThreshold))
			Expect(actualPvc.Annotations[pvaconstants.AnnotationMaxCapacity]).To(Equal(maxAllowed))
		})

		It("should only affect PVCs which match the specified selector", func() {
			Expect(fakeClient.Create(ctx, &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName2,
					Namespace: namespace,
					Labels: map[string]string{
						pvcLabelKey: pvcLabelValue2,
					},
				},
			})).To(Succeed())
			Expect(monitoringutils.EnableAutoscalingOnExistingPVCs(
				ctx, fakeClient, namespace, true, maxAllowed, map[string]string{pvcLabelKey: pvcLabelValue})).
				To(Succeed())

			var actualPvc corev1.PersistentVolumeClaim
			Expect(fakeClient.Get(ctx, client.ObjectKey{namespace, pvcName2}, &actualPvc)).To(Succeed())
			Expect(actualPvc.Annotations).To(BeNil())
		})

		It("should not modify a PVC if it already has existing autoscaling configuration", func() {
			Expect(fakeClient.Create(ctx, &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: namespace,
					Labels: map[string]string{
						pvcLabelKey: pvcLabelValue,
					},
					Annotations: map[string]string{pvaconstants.AnnotationIsEnabled: "false"},
				},
			})).To(Succeed())

			Expect(monitoringutils.EnableAutoscalingOnExistingPVCs(
				ctx, fakeClient, namespace, true, maxAllowed, map[string]string{pvcLabelKey: pvcLabelValue})).
				To(Succeed())

			var actualPvc corev1.PersistentVolumeClaim
			Expect(fakeClient.Get(ctx, client.ObjectKey{namespace, pvcName}, &actualPvc)).To(Succeed())
			Expect(actualPvc.Annotations).NotTo(BeNil())
			Expect(actualPvc.Annotations[pvaconstants.AnnotationIsEnabled]).To(Equal("false"))
			Expect(actualPvc.Annotations).NotTo(HaveKey(pvaconstants.AnnotationMaxCapacity))
		})

		It("should not to modify max-capacity if the PVC already has it configured", func() {
			Expect(fakeClient.Create(ctx, &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: namespace,
					Labels: map[string]string{
						pvcLabelKey: pvcLabelValue,
					},
					Annotations: map[string]string{pvaconstants.AnnotationMaxCapacity: maxAllowed},
				},
			})).To(Succeed())

			Expect(monitoringutils.EnableAutoscalingOnExistingPVCs(
				ctx, fakeClient, namespace, true, "1Gi", map[string]string{pvcLabelKey: pvcLabelValue})).
				To(Succeed())

			var actualPvc corev1.PersistentVolumeClaim
			Expect(fakeClient.Get(ctx, client.ObjectKey{namespace, pvcName}, &actualPvc)).To(Succeed())
			Expect(actualPvc.Annotations).NotTo(BeNil())
			Expect(actualPvc.Annotations[pvaconstants.AnnotationIsEnabled]).To(Equal("true"))
			Expect(actualPvc.Annotations[pvaconstants.AnnotationMaxCapacity]).To(Equal(maxAllowed))
		})

		It("should configure applicable PVCs, even if some PVCs were skipped due to preexisting configuration", func() {
			Expect(fakeClient.Create(ctx, &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: namespace,
					Labels: map[string]string{
						pvcLabelKey: pvcLabelValue,
					},
					Annotations: map[string]string{pvaconstants.AnnotationIsEnabled: "false"},
				},
			})).To(Succeed())
			Expect(fakeClient.Create(ctx, &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName2,
					Namespace: namespace,
					Labels: map[string]string{
						pvcLabelKey: pvcLabelValue,
					},
				},
			})).To(Succeed())

			Expect(monitoringutils.EnableAutoscalingOnExistingPVCs(
				ctx, fakeClient, namespace, true, maxAllowed, map[string]string{pvcLabelKey: pvcLabelValue})).
				To(Succeed())

			var actualPvc corev1.PersistentVolumeClaim
			Expect(fakeClient.Get(ctx, client.ObjectKey{namespace, pvcName}, &actualPvc)).To(Succeed())
			Expect(actualPvc.Annotations).NotTo(BeNil())
			Expect(actualPvc.Annotations[pvaconstants.AnnotationIsEnabled]).To(Equal("false"))

			Expect(fakeClient.Get(ctx, client.ObjectKey{namespace, pvcName2}, &actualPvc)).To(Succeed())
			Expect(actualPvc.Annotations).NotTo(BeNil())
			Expect(actualPvc.Annotations[pvaconstants.AnnotationIsEnabled]).To(Equal("true"))
			Expect(actualPvc.Annotations[pvaconstants.AnnotationMaxCapacity]).To(Equal(maxAllowed))
		})
	})
})
