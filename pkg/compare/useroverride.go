package compare

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	jsonpatch "github.com/evanphx/json-patch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type patchType string

const (
	mergePatch = "mergepatch"
	rfc6902    = "rfc6902"
)

type UserOverride struct {
	Name       string    `json:"name"`
	ApiVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace"`
	ExactMatch string    `json:"exactMatch"`
	Type       patchType `json:"type"`
	Patch      string    `json:"patch"`
}

func (o UserOverride) GetName() string {
	return o.Name
}

func (o UserOverride) GetMetadata() (*unstructured.Unstructured, error) {
	metadata := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": o.ApiVersion,
		"kind":       o.Kind,
		"metadata": map[string]any{
			"name":      o.Name,
			"namespace": o.Namespace,
		},
	}}
	return &metadata, nil
}

func applyPatch(data, patch []byte, patchType patchType) ([]byte, error) {
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
	}
	return data, fmt.Errorf("unknown patch type: %s", patchType)
}

func (o UserOverride) Apply(resource *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	data, err := json.Marshal(resource)
	if err != nil {
		return resource, fmt.Errorf("failed to marshal reference CR: %w", err)
	}

	modified, err := applyPatch(data, []byte(o.Patch), o.Type)
	if err != nil {
		return resource, err
	}

	updatedObj := make(map[string]any)
	err = json.Unmarshal(modified, &updatedObj)
	if err != nil {
		return resource, fmt.Errorf("failed to unmarshal updated manifest: %w", err)
	}
	return &unstructured.Unstructured{Object: updatedObj}, nil
}

func CreateMergePatch(obj InfoObject) (*UserOverride, error) {
	localRefRuntime, err := obj.Merged()
	if err != nil {
		return nil, fmt.Errorf("failed to create patch: %w", err)
	}
	localRef, ok := localRefRuntime.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("failed to create patch: couldn't type cast type %T to *unstructured.Unstructured", localRef)
	}
	localRefData, err := json.Marshal(localRef)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reference CR: %w", err)
	}
	clusterCR, ok := obj.Live().(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("failed to create patch: couldn't type cast type %T to *unstructured.Unstructured", obj.Live())
	}
	clusterCRData, err := json.Marshal(clusterCR)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster CR: %w", err)
	}

	patch, err := jsonpatch.CreateMergePatch(localRefData, clusterCRData)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch: %w", err)
	}

	override := UserOverride{
		Name:       clusterCR.GetName(),
		ApiVersion: clusterCR.GetAPIVersion(),
		Kind:       clusterCR.GetKind(),
		Namespace:  clusterCR.GetNamespace(),
		Type:       mergePatch,
		Patch:      string(patch),
	}

	return &override, nil
}

func loadUserOverrides(path string) ([]*UserOverride, error) {
	result := make([]*UserOverride, 0)

	contents, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("failed to load user overrides: %w", err)
	}

	err = json.Unmarshal(contents, &result)
	if err != nil {
		return result, fmt.Errorf("failed to load user overrides: %w", err)
	}

	return result, nil
}

func dumpOverrides(overrides []*UserOverride, fw io.Writer) (int, error) {
	contents, err := json.Marshal(overrides)
	if err != nil {
		return 0, fmt.Errorf("failed to dump overrides: %w", err)
	}
	n, err := fw.Write(contents)
	if err != nil {
		return n, fmt.Errorf("failed to dump overrides: %w", err)
	}
	return n, nil
}
