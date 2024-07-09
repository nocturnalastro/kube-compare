// SPDX-License-Identifier:Apache-2.0

package compare

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

type Reference struct {
	Parts                 []Part       `json:"parts"`
	TemplateFunctionFiles []string     `json:"templateFunctionFiles,omitempty"`
	FieldsToOmit          FieldsToOmit `json:"fieldsToOmit,omitempty"`
}

type Part struct {
	Name       string      `json:"name"`
	Components []Component `json:"components"`
}

type ComponentType string

const (
	Required ComponentType = "Required"
	Optional ComponentType = "Optional"
)

const (
	fieldsToOmitBuiltInOverwritten = `fieldsToOmit.Map contains the key "%s", this will be overwritten with default values`
	fieldsToOmitDefaultNotFound    = `fieldsToOmit's defaultKey "%s" not found in items`
	fieldsToOmitRefsNotFound       = `skipping fieldsToOmitRefs entry: "%s" not found it fieldsToOmit Items`
)

type FieldsToOmitConfig struct {
	DefaultKey string `json:"defaultKey,omitempty"`
}

type FieldsToOmit struct {
	Config         FieldsToOmitConfig  `json:"config,omitempty"`
	Items          map[string][]string `json:"items,omitempty"`
	processedItems map[string][]Path
}

// Setup FieldsToOmit to be used by setting defaults
// and processing the item strings into paths
func (toOmit *FieldsToOmit) process() error {
	if toOmit.Items == nil {
		toOmit.Items = make(map[string][]string)
	}

	if _, ok := toOmit.Items[builtInPathsKey]; ok {
		klog.Warningf(fieldsToOmitBuiltInOverwritten, builtInPathsKey)
	}

	toOmit.Items[builtInPathsKey] = builtInPaths

	if toOmit.Config.DefaultKey == "" {
		toOmit.Config.DefaultKey = builtInPathsKey
	}

	if _, ok := toOmit.Items[toOmit.Config.DefaultKey]; !ok {
		return fmt.Errorf(fieldsToOmitDefaultNotFound, toOmit.Config.DefaultKey)
	}

	toOmit.processedItems = make(map[string][]Path)
	for key, pathsArray := range toOmit.Items {
		processedPaths := make([]Path, 0)
		for _, p := range pathsArray {
			path, err := NewPath(p)
			if err != nil {
				klog.Errorf("skipping path: %s", err)
				continue
			}
			processedPaths = append(processedPaths, path)
		}
		toOmit.processedItems[key] = processedPaths

	}
	return nil
}

type Component struct {
	Name              string               `json:"name"`
	Type              ComponentType        `json:"type,omitempty"`
	RequiredTemplates []*ReferenceTemplate `json:"requiredTemplates,omitempty"`
	OptionalTemplates []*ReferenceTemplate `json:"optionalTemplates,omitempty"`
}
type ReferenceTemplateConfig struct {
	AllowMerge       bool     `json:"ignore-unspecified-fields,omitempty"`
	FieldsToOmitRefs []string `json:"fieldsToOmitRefs,omitempty"`
}

type ReferenceTemplate struct {
	*template.Template
	Path   string                  `json:"path"`
	Config ReferenceTemplateConfig `json:"config,omitempty"`
}

func (rf ReferenceTemplate) FeildsToOmit(fieldsToOmit FieldsToOmit) []Path {
	result := make([]Path, 0)
	if len(rf.Config.FieldsToOmitRefs) == 0 {
		return fieldsToOmit.processedItems[fieldsToOmit.Config.DefaultKey]
	}

	for _, feildsRef := range rf.Config.FieldsToOmitRefs {
		if feilds, ok := fieldsToOmit.processedItems[feildsRef]; ok {
			result = append(result, feilds...)
		} else {
			klog.Warningf(fieldsToOmitRefsNotFound, feildsRef)
		}
	}
	return result
}

func (rf ReferenceTemplate) Exec(params map[string]any) (*unstructured.Unstructured, error) {
	return executeYAMLTemplate(rf.Template, params)
}

func (r *Reference) getTemplates() []*ReferenceTemplate {
	var templates []*ReferenceTemplate
	for _, part := range r.Parts {
		for _, comp := range part.Components {
			templates = append(templates, comp.RequiredTemplates...)
			templates = append(templates, comp.OptionalTemplates...)
		}
	}
	return templates
}

func (c *Component) getMissingCRs(matchedTemplates map[string]bool) []string {
	var crs []string
	for _, temp := range c.RequiredTemplates {
		if wasMatched := matchedTemplates[temp.Path]; !wasMatched {
			crs = append(crs, temp.Path)
		}
	}
	return crs
}

func (p *Part) getMissingCRs(matchedTemplates map[string]bool) (map[string][]string, int) {
	crs := make(map[string][]string)
	count := 0
	for _, comp := range p.Components {
		compCRs := comp.getMissingCRs(matchedTemplates)
		if (len(compCRs) > 0) && (comp.Type == Required || ((comp.Type == Optional) && len(compCRs) != len(comp.RequiredTemplates))) {
			crs[comp.Name] = compCRs
			count += len(compCRs)
		}
	}
	return crs, count
}

func (r *Reference) getMissingCRs(matchedTemplates map[string]bool) (map[string]map[string][]string, int) {
	crs := make(map[string]map[string][]string)
	count := 0
	for _, part := range r.Parts {
		crsInPart, countInPart := part.getMissingCRs(matchedTemplates)
		if countInPart > 0 {
			crs[part.Name] = crsInPart
			count += countInPart
		}
	}
	return crs, count
}

const builtInPathsKey = "cluster-compare-built-in"

var builtInPaths = []string{
	"metadata.resourceVersion",
	"metadata.generation",
	"metadata.uid",
	"metadata.generateName",
	"metadata.creationTimestamp",
	"metadata.finalizers",
	`"kubectl.kubernetes.io/last-applied-configuration"`,
	`metadata.annotations."kubectl.kubernetes.io/last-applied-configuration"`,
	"status",
}

type PathPart struct {
	part  string
	regex *regexp.Regexp
}

type Path struct {
	parts []PathPart
}

func NewPath(path string) (Path, error) {
	parts, err := splitFields(path)
	return Path{parts: parts}, err
}

// splitFields splits a dot delmited path into parts
//
// unless the dot is within quotes
func splitFields(path string) ([]PathPart, error) {
	result := make([]PathPart, 0)

	path = strings.TrimLeft(path, ".")
	// 1. Find all sets of quotes
	quotes := regexp.MustCompile("(?U:([\"|`].*[\"|`]))")
	// TODO: Support single quotes

	matches := quotes.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		for _, s := range strings.Split(path, ".") {
			result = append(result, PathPart{part: s})
		}
		return result, nil
	}

	// 2. replace quoted blocks with placeholder text
	replaced := make(map[string]string)
	newPath := path
	for n, matchParts := range matches {
		v := fmt.Sprintf("cluster-compare-entry-%d", n)
		replaced[v] = matchParts[1]
		newPath = strings.Replace(newPath, matchParts[1], v, 1)
	}

	if len(quotes.FindAllStringSubmatch(newPath, -1)) > 0 {
		return result, fmt.Errorf("failed to remove quotes from path %s", path)
	}
	// 3. split string with place holders on dots
	splitPath := strings.Split(newPath, ".")
	// 4. replace place holder with origonal text without the quotes
	var err error
	for _, e := range splitPath {
		p := PathPart{part: e}
		if x, ok := replaced[e]; ok {
			if strings.HasPrefix(x, "`") && strings.HasSuffix(x, "`") {
				p.part = strings.Trim(x, "`")
				p.regex, err = regexp.Compile(p.part)
				if err != nil {
					return result, fmt.Errorf("failed to compile regex for part `%s` in path %s: %w", p.part, path, err)
				}
			} else {
				p.part = strings.Trim(x, `"`)
			}
		}
		result = append(result, p)
	}
	return result, nil
}

const (
	refConfNotExistsError          = "Reference config file not found. error: %w"
	refConfigNotInFormat           = "Reference config isn't in correct format. error: %w"
	userConfNotExistsError         = "User Config File not found. error: %w"
	userConfigNotInFormat          = "User config file isn't in correct format. error: %w"
	templatesCantBeParsed          = "an error occurred while parsing template: %s specified in the config. error: %w"
	templatesFunctionsCantBeParsed = "an error occurred while parsing the template function files specified in the config. error: %w"
)

func getReference(fsys fs.FS) (Reference, error) {
	result := Reference{}
	err := parseYaml(fsys, ReferenceFileName, &result, refConfNotExistsError, refConfigNotInFormat)
	if err != nil {
		return result, err
	}

	if result.FieldsToOmit.Items == nil {
		result.FieldsToOmit.Items = make(map[string][]string)
	}

	err = result.FieldsToOmit.process()
	if err != nil {
		return result, err
	}

	return result, nil
}

func parseYaml[T any](fsys fs.FS, filePath string, structType *T, fileNotFoundError, parsingError string) error {
	file, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		return fmt.Errorf(fileNotFoundError, err)
	}
	err = yaml.UnmarshalStrict(file, structType)
	if err != nil {
		return fmt.Errorf(parsingError, err)
	}
	return nil
}

func parseTemplates(templateReference []*ReferenceTemplate, functionTemplates []string, fsys fs.FS) ([]*ReferenceTemplate, error) {
	var errs []error
	for _, temp := range templateReference {
		parsedTemp, err := template.New(path.Base(temp.Path)).Funcs(FuncMap()).ParseFS(fsys, temp.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf(templatesCantBeParsed, temp.Path, err))
			continue
		}
		// recreate template with new name that includes path from reference root:
		parsedTemp, _ = template.New(temp.Path).Funcs(FuncMap()).AddParseTree(temp.Path, parsedTemp.Tree)
		if len(functionTemplates) > 0 {
			parsedTemp, err = parsedTemp.ParseFS(fsys, functionTemplates...)
			if err != nil {
				errs = append(errs, fmt.Errorf(templatesFunctionsCantBeParsed, err))
				continue
			}
		}
		temp.Template = parsedTemp
	}
	return templateReference, errors.Join(errs...) // nolint:wrapcheck
}

type UserConfig struct {
	CorrelationSettings CorrelationSettings `json:"correlationSettings"`
}

type CorrelationSettings struct {
	ManualCorrelation ManualCorrelation `json:"manualCorrelation"`
}

type ManualCorrelation struct {
	CorrelationPairs map[string]string `json:"correlationPairs"`
}

func parseDiffConfig(filePath string) (UserConfig, error) {
	result := UserConfig{}
	confPath, err := filepath.Abs(filePath)
	if err != nil {
		return result, fmt.Errorf("failed to get absolute path for %s: %w", filePath, err)
	}
	err = parseYaml(os.DirFS("/"), confPath[1:], &result, userConfNotExistsError, userConfigNotInFormat)
	return result, err
}

const noValue = "<no value>"

func executeYAMLTemplate(temp *template.Template, params map[string]any) (*unstructured.Unstructured, error) {
	var buf bytes.Buffer
	err := temp.Execute(&buf, params)
	if err != nil {
		return nil, fmt.Errorf("failed to constuct template: %w", err)
	}
	data := make(map[string]any)
	content := buf.Bytes()
	err = yaml.Unmarshal(bytes.ReplaceAll(content, []byte(noValue), []byte("")), &data)
	if err != nil {
		return nil, fmt.Errorf("template: %s isn't a yaml file after injection. yaml unmarshal error: %w. The Template After Execution: %s", temp.Name(), err, string(content))
	}
	return &unstructured.Unstructured{Object: data}, nil
}
func extractMetadata(t *ReferenceTemplate) (*unstructured.Unstructured, error) {
	yamlTemplate, err := t.Exec(map[string]any{})
	return yamlTemplate, err
}
