package gardenercustommetrics

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed test_data/templates/*.yaml
var testEmbeddedTemplateFiles embed.FS

var _ = Describe("GardenerCustomMetrics", func() {
	const (
		testNamespace = "my-namespace"
		testImage     = "my-container"
	)
	var (
		expectedManifests = []string{`
{
	"kind":"my-kind",
	"metadata":{
		"containerImageName":"my-container,",
		"deploymentName":"gardener-custom-metrics,",
		"name":"my-name",
		"namespace":"my-namespace",
		"serverSecretName":"my-secret,"
	}
}`,
			`
{
	"kind":"my-kind",
	"metadata":{
		"name":"my-name-2",
		"namespace":"my-namespace"
	}
}`,
		}
	)
	Describe("LoadTemplates()", func() {
		It("should successfully load a manifest template", func() {
			// Arrange
			mr := manifestReader{}

			// Act and assert
			Expect(mr.LoadTemplates(testEmbeddedTemplateFiles)).To(Succeed())
		})
	})

	Describe("GetManifests()", func() {
		It("should correctly process a manifest template", func() {
			// Arrange
			secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my-secret"}}
			mr := manifestReader{}
			Expect(mr.LoadTemplates(testEmbeddedTemplateFiles)).To(Succeed())

			// Act
			manifests, err := mr.GetManifests(testNamespace, testImage, &secret)

			// Assert
			Expect(err).NotTo(HaveOccurred())
			Expect(manifests).To(HaveLen(2))
			for i, manifest := range manifests {
				By(fmt.Sprintf("manifests[%d]", i), func() {
					reader, err := manifest.Read()
					Expect(err).NotTo(HaveOccurred())
					jsonBytes, err := json.Marshal(reader)
					Expect(err).NotTo(HaveOccurred())
					jsonString := string(jsonBytes)

					var compactExpected bytes.Buffer
					Expect(json.Compact(&compactExpected, []byte(expectedManifests[i]))).To(Succeed())
					Expect(jsonString).To(Equal(compactExpected.String()))
				})
			}
		})
	})
})
