package gardenercustommetrics

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener/pkg/client/kubernetes"
	utilerrors "github.com/gardener/gardener/pkg/utils/errors"
)

// Templates for the k8s resources realising GCMx. A template is actually not limited to a single resource, but rather
// corresponds to a manifest, potentially containing multiple resources.
//
//go:embed templates/*.yaml
var resourceTemplateFiles embed.FS
var resourceTemplates []*template.Template // resourceTemplateFiles loaded into Template objects

func init() {
	baseErrorMessage := "An error occurred while loading resource templates for the GardenerCustomMetrics component"

	// Load the k8s resource templates
	fs.WalkDir(resourceTemplateFiles, ".", func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf(baseErrorMessage+". The error message reported by the underlying operation follows: %w", err)
		}
		if dirEntry.IsDir() {
			// Do nothing. We don't expect there to be directories, but if we passed tests,
			// it's likely safe to ignore them.
			return nil
		}

		bytes, err := resourceTemplateFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf(
				baseErrorMessage+" - reading file '%s' failed. "+
					"The error message reported by the underlying operation follows: %w",
				path,
				err)
		}

		template, err := template.
			New(dirEntry.Name()).
			Funcs(sprig.TxtFuncMap()).
			Parse(string(bytes))
		if err != nil {
			return fmt.Errorf(
				baseErrorMessage+" - parsing template file '%s' failed. "+
					"The error message reported by the underlying operation follows: %w",
				path,
				err)
		}

		resourceTemplates = append(resourceTemplates, template)
		return nil
	})
}

// Formats all GCMx resource manifest templates, based on the specified parameters, and returns them in the form of
// reader objects
func getManifests(
	namespaceName string,
	containerImageName string,
	serverCertificateSecret *corev1.Secret) ([]kubernetes.UnstructuredReader, error) {

	templateParams := struct {
		ContainerImageName string
		DeploymentName     string
		Namespace          string
		ServerSecretName   string
	}{
		ContainerImageName: containerImageName,
		DeploymentName:     deploymentName,
		Namespace:          namespaceName,
		ServerSecretName:   serverCertificateSecret.Name,
	}

	// Execute each manifest template and get object reader for the resulting raw output
	var formattedManifests []kubernetes.UnstructuredReader
	for i, template := range resourceTemplates {
		var formattedManifest bytes.Buffer
		if err := template.Execute(&formattedManifest, templateParams); err != nil {
			return nil, fmt.Errorf(
				"An error occurred while retrieving resource manifests for the GardenerCustomMetics component - "+
					"executing the template at index %d failed. "+
					"The error message reported by the underlying operation follows: %w",
				i,
				err)
		}
		formattedManifests = append(formattedManifests, kubernetes.NewManifestReader(formattedManifest.Bytes()))
	}

	return formattedManifests, nil
}

// Reads and returns all objects from the specified manifestReader
func readManifest(manifestReader kubernetes.UnstructuredReader) ([]client.Object, error) {
	var objectsRead []client.Object
	allErrors := &multierror.Error{
		ErrorFormat: utilerrors.NewErrorFormatFuncWithPrefix(
			"failed to read manifests for the GardenerCustomMetrics component"),
	}

	for {
		obj, err := manifestReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			allErrors = multierror.Append(allErrors, fmt.Errorf("could not read object: %+v", err))
			continue
		}
		if obj == nil {
			continue
		}

		objectsRead = append(objectsRead, obj)
	}

	return objectsRead, allErrors.ErrorOrNil()
}
