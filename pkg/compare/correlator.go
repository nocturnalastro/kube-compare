// SPDX-License-Identifier:Apache-2.0

package compare

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

var fieldSeparator = "_"

// Correlator provides an abstraction that allow the usage of different Resource correlation logics
// in the kubectl cluster-compare. The correlation process Matches for each Resource a template.
type Correlator[T CorrelationEntry] interface {
	Match(*unstructured.Unstructured) ([]T, error)
}

// UnknownMatch an error that can be returned by a Correlator in a case no template was matched for a Resource.
type UnknownMatch struct {
	Resource *unstructured.Unstructured
}

func (e UnknownMatch) Error() string {
	return fmt.Sprintf("Template couldn't be matched for: %s", apiKindNamespaceName(e.Resource))
}

func apiKindNamespaceName(r *unstructured.Unstructured) string {
	if r.GetNamespace() == "" {
		return strings.Join([]string{r.GetAPIVersion(), r.GetKind(), r.GetName()}, fieldSeparator)
	}
	return strings.Join([]string{r.GetAPIVersion(), r.GetKind(), r.GetNamespace(), r.GetName()}, fieldSeparator)
}

// MultipleMatches an error that can be returned by a Correlator in a case multiple template Matches were found for a Resource.
type MultipleMatches[T CorrelationEntry] struct {
	Resource *unstructured.Unstructured
	Matches  []T
}

func (e MultipleMatches[T]) Error() string {
	return fmt.Sprintf("Multiple matches were found for: %s. The matches found are: %s", apiKindNamespaceName(e.Resource), getTemplatesNames(e.Matches))
}

// MultiCorrelator Matches templates by attempting to find a match with one of its predefined Correlators.
type MultiCorrelator[T CorrelationEntry] struct {
	correlators []Correlator[T]
}

func NewMultiCorrelator[T CorrelationEntry](correlators []Correlator[T]) Correlator[T] {
	return &MultiCorrelator[T]{correlators: correlators}
}

func (c MultiCorrelator[T]) Match(object *unstructured.Unstructured) ([]T, error) {
	var errs []error
	for _, core := range c.correlators {
		temp, err := core.Match(object)
		if err == nil || !errors.As(err, &UnknownMatch{}) {
			return temp, err // nolint:wrapcheck
		}
		errs = append(errs, err)
	}
	var res []T
	return res, errors.Join(errs...) // nolint:wrapcheck
}

type CorrelationEntry interface {
	GetName() string
	GetMetadata() *unstructured.Unstructured
}

// ExactMatchCorrelator Matches templates by exact match between a predefined config including pairs of Resource names and there equivalent template.
// The names of the resources are in the apiVersion-kind-namespace-name format.
// For fields that are not namespaced apiVersion-kind-name format will be used.
type ExactMatchCorrelator[T any] struct {
	apiKindNamespaceName map[string]T
}

func NewExactMatchCorrelator[T CorrelationEntry](matchPairs map[string]string, templates []T) (*ExactMatchCorrelator[T], error) {
	core := ExactMatchCorrelator[T]{}
	core.apiKindNamespaceName = make(map[string]T)
	nameToObject := make(map[string]T)
	for _, temp := range templates {
		nameToObject[temp.GetName()] = temp
	}
	for cr, temp := range matchPairs {
		obj, ok := nameToObject[temp]
		if !ok {
			return nil, fmt.Errorf("error in template manual matching for resource: %s no template in the name of %s", cr, temp)
		}
		core.apiKindNamespaceName[cr] = obj

	}
	return &core, nil
}

func (c ExactMatchCorrelator[T]) Match(object *unstructured.Unstructured) ([]T, error) {
	temp, ok := c.apiKindNamespaceName[apiKindNamespaceName(object)]
	if !ok {
		return []T{}, UnknownMatch{Resource: object}
	}
	return []T{temp}, nil
}

func fetchMetadata[T CorrelationEntry](t T) (*unstructured.Unstructured, error) {
	return t.GetMetadata(), nil //nolint: wrapcheck
}

// GroupCorrelator Matches templates by hashing predefined fields.
// All The templates are indexed by  hashing groups of `indexed` fields. The `indexed` fields can be nested.
// Resources will be attempted to be matched with hashing by the group with the largest amount of `indexed` fields.
// In case a Resource Matches by a hash a group of templates the group correlator will continue looking for a match
// (with groups with less `indexed fields`) until it finds a distinct match, in case it doesn't, MultipleMatches error
// will be returned.
// Templates will be only indexed by a group of fields only if all fields in group are not templated.
type GroupCorrelator[T CorrelationEntry] struct {
	fieldCorrelators []*FieldCorrelator[T]
}

// NewGroupCorrelator creates a new GroupCorrelator using inputted fieldGroups and generated GroupFunctions and templatesByGroups.
// The templates will be divided into different kinds of groups based on the fields that are templated. Templates will be added
// to the kind of group that contains the biggest amount of fully defined `indexed` fields.
// For fieldsGroups =  {{{"metadata", "namespace"}, {"kind"}}, {{"kind"}}} and the following templates: [fixedKindTemplate, fixedNamespaceKindTemplate]
// the fixedNamespaceKindTemplate will be added to a mapping where the keys are  in the format of `namespace_kind`. The fixedKindTemplate
// will be added to a mapping where the keys are  in the format of `kind`.
func NewGroupCorrelator[T CorrelationEntry](fieldGroups [][][]string, objects []T) (*GroupCorrelator[T], error) {
	sort.Slice(fieldGroups, func(i, j int) bool {
		return len(fieldGroups[i]) >= len(fieldGroups[j])
	})
	core := GroupCorrelator[T]{}
	for _, group := range fieldGroups {
		fc := FieldCorrelator[T]{Fields: group, hashFunc: createGroupHashFunc(group)}
		newObjects := fc.ClaimTemplates(objects)

		// Ignore if the fc didn't take any objects
		if len(newObjects) == len(objects) {
			continue
		}

		objects = newObjects
		core.fieldCorrelators = append(core.fieldCorrelators, &fc)

		err := fc.ValidateTemplates()
		if err != nil {
			klog.Warning(err)
		}

		if len(objects) == 0 {
			break
		}
	}

	return &core, nil
}

func getFields(fields [][]string) string {
	var stringifiedFields []string
	for _, field := range fields {
		stringifiedFields = append(stringifiedFields, strings.Join(field, fieldSeparator))
	}
	return strings.Join(stringifiedFields, ", ")
}

type templateHashFunc func(*unstructured.Unstructured, string) (group string, err error)

// createGroupHashFunc creates a hashing function for a specific field group
func createGroupHashFunc(fieldGroup [][]string) templateHashFunc {
	groupHashFunc := func(cr *unstructured.Unstructured, replaceEmptyWith string) (group string, err error) {
		var values []string
		for _, fields := range fieldGroup {
			value, isFound, NotStringErr := unstructured.NestedString(cr.Object, fields...)
			if !isFound || value == "" {
				return "", fmt.Errorf("the field %s doesn't exist in resource", strings.Join(fields, fieldSeparator))
			}
			if NotStringErr != nil {
				return "", fmt.Errorf("the field %s isn't string - grouping by non string values isn't supported", strings.Join(fields, fieldSeparator))
			}
			values = append(values, value)
		}
		return strings.Join(values, fieldSeparator), nil
	}
	return groupHashFunc
}

func getTemplatesNames[T CorrelationEntry](templates []T) string {
	var names []string
	for _, temp := range templates {
		names = append(names, temp.GetName())
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (c *GroupCorrelator[T]) Match(object *unstructured.Unstructured) ([]T, error) {
	var multipleMatchError error
	for _, fc := range c.fieldCorrelators {
		temp, err := fc.Match(object)
		if err != nil {
			if errors.As(err, &MultipleMatches[T]{}) && multipleMatchError == nil {
				multipleMatchError = err
			}
			continue
		}
		if len(temp) == 1 {
			return temp, nil
		}
	}
	if multipleMatchError != nil {
		return nil, multipleMatchError
	}
	return []T{}, UnknownMatch{Resource: object}
}

// MetricsCorrelatorDecorator Matches templates by using an existing correlator and gathers summary info related the correlation.
type MetricsCorrelatorDecorator struct {
	correlator            *Correlator[*ReferenceTemplate]
	UnMatchedCRs          []*unstructured.Unstructured
	unMatchedLock         sync.Mutex
	MatchedTemplatesNames map[string]bool
	matchedLock           sync.Mutex
	parts                 []Part
	errsToIgnore          []error
}

func NewMetricsCorrelatorDecorator(correlator Correlator[*ReferenceTemplate], parts []Part, errsToIgnore []error) *MetricsCorrelatorDecorator {
	cr := MetricsCorrelatorDecorator{
		correlator:            &correlator,
		UnMatchedCRs:          []*unstructured.Unstructured{},
		MatchedTemplatesNames: map[string]bool{},
		parts:                 parts,
		errsToIgnore:          errsToIgnore,
	}
	return &cr
}

func (c *MetricsCorrelatorDecorator) Match(object *unstructured.Unstructured) (*ReferenceTemplate, error) {
	temps, err := (*c.correlator).Match(object)
	if err != nil && !containOnly(err, c.errsToIgnore) {
		c.addUNMatch(object)
	}
	if len(temps) > 1 {
		err = MultipleMatches[*ReferenceTemplate]{Resource: object, Matches: temps}
		c.addUNMatch(object)
	}

	if err != nil {
		return nil, err // nolint:wrapcheck
	}

	temp := temps[0]
	c.addMatch(temp)
	return temp, nil
}

// containOnly checks if at least one of the joined errors isn't from the err-types passed in errTypes
func containOnly(err error, errTypes []error) bool {
	var errs []error
	joinedErr, isJoined := err.(interface{ Unwrap() []error })
	if isJoined {
		errs = joinedErr.Unwrap()
	} else {
		errs = []error{err}
	}
	for _, errPart := range errs {
		c := false
		for _, errType := range errTypes {
			if reflect.TypeOf(errType).Name() == reflect.TypeOf(errPart).Name() {
				c = true
			}
		}
		if !c {
			return false
		}
	}
	return true
}

func (c *MetricsCorrelatorDecorator) addMatch(temp *ReferenceTemplate) {
	c.matchedLock.Lock()
	c.MatchedTemplatesNames[temp.GetName()] = true
	c.matchedLock.Unlock()
}

func (c *MetricsCorrelatorDecorator) addUNMatch(cr *unstructured.Unstructured) {
	c.unMatchedLock.Lock()
	c.UnMatchedCRs = append(c.UnMatchedCRs, cr)
	c.unMatchedLock.Unlock()
}

type FieldCorrelator[T CorrelationEntry] struct {
	Fields   [][]string
	hashFunc templateHashFunc
	objects  map[string][]T
}

func (f *FieldCorrelator[T]) ClaimTemplates(templates []T) []T {
	if f.objects == nil {
		f.objects = make(map[string][]T)
	}

	discarded := make([]T, 0)
	for _, temp := range templates {
		md := temp.GetMetadata()
		hash, err := f.hashFunc(md, noValue)
		if err != nil || strings.Contains(hash, noValue) {
			discarded = append(discarded, temp)
		} else {
			f.objects[hash] = append(f.objects[hash], temp)
		}
	}

	return discarded
}

func (f *FieldCorrelator[T]) ValidateTemplates() error {
	errs := make([]error, 0)
	for _, values := range f.objects {
		if len(values) > 1 {
			errs = append(errs, fmt.Errorf(
				"More then one template with same %s. These templates wont be used for correlation. "+
					"To use them use different correlator (manual matching) or remove one of them from the reference. "+
					"Template names are: %s",
				getFields(f.Fields), getTemplatesNames(values)),
			)
		}
	}

	return errors.Join(errs...)
}

func (f FieldCorrelator[T]) Match(object *unstructured.Unstructured) ([]T, error) {
	group_hash, err := f.hashFunc(object, "")
	if err != nil {
		return nil, err
	}
	objs, ok := f.objects[group_hash]
	if !ok {
		return nil, UnknownMatch{Resource: object}
	}
	if len(objs) > 1 {
		return nil, MultipleMatches[T]{Resource: object, Matches: objs}
	}
	return objs, nil
}
