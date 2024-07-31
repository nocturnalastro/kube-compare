package compare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/template"

	jsonpatch "github.com/evanphx/json-patch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type patchType string

const (
	mergePatch = "mergepatch"
	rfc6902    = "rfc6902"
	gotemplate = "go-template"
)

type UserOverride struct {
	Name           string    `json:"name,omitempty"`
	ApiVersion     string    `json:"apiVersion,omitempty"`
	Kind           string    `json:"kind,omitempty"`
	Namespace      string    `json:"namespace,omitempty"`
	ExactMatch     string    `json:"exactMatch,omitempty"`
	Type           patchType `json:"type"`
	Patch          string    `json:"patch"`
	ReferenceValue string    `json:"referenceValue,omitempty"`
	ClusterValue   string    `json:"clusterValue,omitempty"`
}

func (o UserOverride) GetName() string {
	return o.Name
}

func (o UserOverride) GetMetadata() *unstructured.Unstructured {
	metadata := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": o.ApiVersion,
		"kind":       o.Kind,
		"metadata": map[string]any{
			"name":      o.Name,
			"namespace": o.Namespace,
		},
	}}
	return &metadata
}

func applyPatch(resource *unstructured.Unstructured, patch []byte, patchType patchType) ([]byte, error) {
	data, err := json.Marshal(resource)
	if err != nil {
		return data, fmt.Errorf("failed to marshal reference CR: %w", err)
	}

	switch patchType {
	case mergePatch:
		modified, err := jsonpatch.MergePatch(data, patch)
		if err != nil {
			return data, fmt.Errorf("failed to apply user patch: %w", err)
		}
		return modified, nil
	case rfc6902:
		decodedPatch, err := jsonpatch.DecodePatch(patch)
		if err != nil {
			return data, fmt.Errorf("failed to decode user patch: %w", err)
		}
		modified, err := decodedPatch.Apply(data)
		if err != nil {
			return data, fmt.Errorf("failed to apply user patch: %w", err)
		}
		return modified, nil
	case gotemplate:
		t, err := template.New("").Parse(string(patch))
		if err != nil {
			return data, fmt.Errorf("failed to parse patch as template: %w", err)
		}
		var buf bytes.Buffer
		err = t.Execute(&buf, resource.Object)
		if err != nil {
			return data, fmt.Errorf("failed to execute patch template: %w", err)
		}
		uo := UserOverride{}
		err = yaml.Unmarshal(buf.Bytes(), &uo)
		if err != nil {
			return data, fmt.Errorf("failed to unmarshal templated patch: %w", err)
		}
		return applyPatch(resource, []byte(uo.Patch), uo.Type)
	}
	return data, fmt.Errorf("unknown patch type: %s", patchType)
}

func (o UserOverride) Apply(resource *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	modified, err := applyPatch(resource, []byte(o.Patch), o.Type)
	if err != nil {
		return resource, err
	}

	updatedObj := make(map[string]any)
	err = yaml.Unmarshal(modified, &updatedObj)
	if err != nil {
		return resource, fmt.Errorf("failed to unmarshal updated manifest: %w", err)
	}
	return &unstructured.Unstructured{Object: updatedObj}, nil
}

func CreateMergePatch(obj InfoObject, diffOutput string) (*UserOverride, error) {
	localRefRuntime, err := obj.Merged()
	if err != nil {
		return nil, fmt.Errorf("failed to create patch: %w", err)
	}
	localRef, ok := localRefRuntime.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("failed to create patch: couldn't type cast type %T to *unstructured.Unstructured", localRef)
	}
	localRefData, err := localRef.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reference CR: %w", err)
	}
	clusterCR, ok := obj.Live().(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("failed to create patch: couldn't type cast type %T to *unstructured.Unstructured", obj.Live())
	}
	clusterCRData, err := clusterCR.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster CR: %w", err)
	}

	patch, err := jsonpatch.CreateMergePatch(localRefData, clusterCRData)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch: %w", err)
	}

	override := UserOverride{
		Name:           clusterCR.GetName(),
		ApiVersion:     clusterCR.GetAPIVersion(),
		Kind:           clusterCR.GetKind(),
		Namespace:      clusterCR.GetNamespace(),
		Type:           mergePatch,
		Patch:          string(patch),
		ReferenceValue: string(localRefData),
		ClusterValue:   string(clusterCRData),
	}

	return &override, nil
}

func LoadUserOverrides(path string) ([]*UserOverride, error) {
	result := make([]*UserOverride, 0)

	contents, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("failed to load user overrides: %w", err)
	}

	err = yaml.Unmarshal(contents, &result)
	if err != nil {
		return result, fmt.Errorf("failed to load user overrides: %w", err)
	}

	return result, nil
}

func DumpOverrides(overrides []*UserOverride, fw io.Writer) (int, error) {
	contents, err := yaml.Marshal(overrides)
	if err != nil {
		return 0, fmt.Errorf("failed to dump overrides: %w", err)
	}
	n, err := fw.Write(contents)
	if err != nil {
		return n, fmt.Errorf("failed to dump overrides: %w", err)
	}
	return n, nil
}
