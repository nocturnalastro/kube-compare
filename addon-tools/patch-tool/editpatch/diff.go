package editpatch

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/openshift/kube-compare/pkg/compare"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

type Diff struct {
	clusterValue   *unstructured.Unstructured
	referenceValue *unstructured.Unstructured
	patchOrigonal  *compare.UserOverride
	patch          *compare.UserOverride
	name           string
	IOStreams      genericiooptions.IOStreams
	output         string
}

func (d Diff) Live() runtime.Object {
	return d.clusterValue
}

func (d Diff) Merged() (runtime.Object, error) {
	if d.patch == nil {
		return d.referenceValue, nil
	}
	return d.patch.Apply(d.referenceValue) // nolint: wrapcheck
}

func (d Diff) Name() string {
	return d.name
}

func (d *Diff) UpdatePatch(patch string) error {
	uo := d.patchOrigonal.Clone()
	var v map[string]any
	err := json.Unmarshal([]byte(patch), &v)
	if err != nil {
		return err // nolint
	}
	p, err := json.Marshal(v)
	if err != nil {
		return err // nolint
	}
	uo.Patch = string(p)
	_, err = uo.Apply(d.referenceValue)
	if err != nil {
		return err // nolint: wrapcheck
	}
	d.output = ""
	d.patch = &uo
	return nil
}

func (d *Diff) ClearPatch() {
	d.patch = nil
	d.output = ""
}

func (d *Diff) IsModified() bool {
	return d.patch != nil
}

func (d *Diff) Run() (*bytes.Buffer, error) {
	if d.output == "" {
		out, err := compare.RunDiff(d, d.IOStreams, false)
		if err != nil {
			return out, fmt.Errorf("%w", err)
		}
		d.output = out.String()
		return out, nil
	}
	var out bytes.Buffer
	out.WriteString(d.output)
	return &out, nil
}

func (d Diff) GetPatch() *compare.UserOverride {
	if d.patch != nil {
		return d.patch
	}
	return d.patchOrigonal
}
