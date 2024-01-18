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
var resourceManifests manifestReader

func init() {
	// Load the k8s resource templates
	if err := resourceManifests.LoadTemplates(resourceTemplateFiles); err != nil {
		panic(err)
	}
}

// manifestReader creates a set of k8s resource objects by formatting a set of template files
type manifestReader struct {
	ResourceTemplates []*template.Template // resourceTemplateFiles loaded into Template objects
}

// LoadTemplates initialises a manifestReader instance by loading a set of text templates from the specified set of
// embedded files. Upon success, the templates are available in the manifestReader.resourceTemplateFiles field.
// LoadTemplates must be called exactly once per instance.
func (mr *manifestReader) LoadTemplates(templateFiles embed.FS) error {
	var err error
	mr.ResourceTemplates, err = readTemplates(templateFiles)
	return err
}

// Formats all GCMx resource manifest templates, based on the specified parameters, and returns them in the form of
// reader objects
func (mr *manifestReader) GetManifests(
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
	for i, template := range mr.ResourceTemplates {
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

//#region Private implementation

// readTemplates reads a set of text templates from the specified set of embedded files.
func readTemplates(templateFiles embed.FS) ([]*template.Template, error) {
	baseErrorMessage := "An error occurred while loading resource templates for the gardener-custom-metrics component"

	var templates []*template.Template
	err := fs.WalkDir(templateFiles, ".", func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf(baseErrorMessage+". The error message reported by the underlying operation follows: %w", err)
		}
		if dirEntry.IsDir() {
			// Do nothing. We don't expect there to be directories, but if we passed tests,
			// it's likely safe to ignore them.
			return nil
		}

		bytes, err := templateFiles.ReadFile(path)
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

		templates = append(templates, template)
		return nil
	})

	if err != nil {
		return nil, err
	}
	return templates, nil
}

// Reads and returns all objects from the specified manifestReader
func readManifest(manifestReader kubernetes.UnstructuredReader) ([]client.Object, error) {
	var objectsRead []client.Object
	allErrors := &multierror.Error{
		ErrorFormat: utilerrors.NewErrorFormatFuncWithPrefix(
			"failed to read manifests for the gardener-custom-metrics component"),
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

//#endregion Private implementation
