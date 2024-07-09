package compare

import (
	"fmt"
	"strconv"
	"strings"
)

// NestedField returns a reference to a nested field.
// Returns false if value is not found and an error if unable
// to traverse obj.
//
// This is a copy of unstructured.NestedFieldNoCopy but can also traverse slices
// If the value is a slice it will try to convert the field into an int
func NestedField(obj any, fields ...string) (any, bool, error) {
	var val any = obj

	for i, field := range fields {
		if val == nil {
			return nil, false, nil
		}

		switch v := val.(type) {
		case map[string]any:
			var ok bool
			val, ok = v[field]
			if !ok {
				return nil, false, nil
			}
		case []any:
			index, err := strconv.Atoi(field)
			if err != nil {
				return nil, false, fmt.Errorf("%v accessor error: found slice but index %s could not be converted into an int: %w", jsonPath(fields[:i+1]), field, err)
			}
			if len(v) >= index {
				return nil, false, nil
			}
			val = v[index]
		default:
			return nil, false, fmt.Errorf("%v accessor error: %v is of the type %T, expected map[string]any or []any", jsonPath(fields[:i+1]), val, val)
		}
	}
	return val, true, nil
}

// RemoveNestedField removes the nested field from the obj.
func RemoveNestedField(obj any, fields ...string) any {
	res, _ := removeNestedFieldBacktrackEmpty(obj, fields...)
	return res
}

func removeNestedFieldBacktrackEmpty(obj any, fields ...string) (a any, empty bool) {
	field := fields[0]

	if len(fields) == 1 {
		return removeField(obj, field)
	}

	switch val := obj.(type) {
	case map[string]any:
		v, ok := val[field]
		if !ok {
			return obj, false
		}
		x, empty := removeNestedFieldBacktrackEmpty(v, fields[1:]...)
		val[field] = x
		if empty {
			delete(val, field)
		}
		return val, len(val) == 0
	case []any:
		index, err := strconv.Atoi(field)
		if err != nil || len(val) <= index {
			return obj, false
		}
		x, empty := removeNestedFieldBacktrackEmpty(val[index], fields[1:]...)
		val[index] = x
		if empty {
			val = val[:index]
			if len(val) > index+1 {
				val = append(val, val[index+1:]...)
			}
		}
		return val, len(val) == 0
	default:
		return obj, false
	}
}

func removeField(obj any, field string) (any, bool) {
	switch v := obj.(type) {
	case map[string]any:
		delete(v, field)
		return v, len(v) == 0
	case []any:
		index, err := strconv.Atoi(field)
		if err != nil || len(v) > index {
			res := v[:index]
			if len(v) > index+1 {
				res = append(res, v[index+1:]...)
			}
			return res, len(res) == 0
		}
	}
	return obj, false
}

func jsonPath(fields []string) string {
	return "." + strings.Join(fields, ".")
}
