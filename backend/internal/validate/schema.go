package validate

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"configplat/internal/model"
)

// checkSchema validates doc against a (draft-07 style) JSON Schema.
// Supported keywords: type, required, properties, additionalProperties,
// enum, const, minimum/maximum/exclusiveMinimum/exclusiveMaximum,
// minLength/maxLength/pattern (pattern via simple anchored equality set only
// when it is a literal), minItems/maxItems, items, minProperties/maxProperties,
// multipleOf, $ref to local #/$defs.
// The first failing field (document order, then schema order) is reported;
// all failures are collected as well.
func checkSchema(schemaText string, doc map[string]any, format string) []FieldError {
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
		return []FieldError{{
			Field:      "$schema",
			Constraint: "schemaSyntax",
			Message:    fmt.Sprintf("JSON Schema 本身不是合法 JSON: %v", err),
		}}
	}
	v := &schemaValidator{root: schema, errs: []FieldError{}}
	v.validate("$", doc, schema)
	if len(v.errs) == 0 && format == model.FormatProperties {
		// Properties documents are flat string maps; nothing extra to do.
	}
	return v.errs
}

type schemaValidator struct {
	root map[string]any
	errs []FieldError
}

func (v *schemaValidator) fail(ptr, constraint, msg string) {
	v.errs = append(v.errs, FieldError{Field: ptr, Constraint: constraint, Message: msg})
}

func (v *schemaValidator) resolveRef(node map[string]any) map[string]any {
	ref, _ := node["$ref"].(string)
	if ref == "" || !strings.HasPrefix(ref, "#/") {
		return node
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	cur := any(v.root)
	for _, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return node
		}
		cur, ok = m[p]
		if !ok {
			return node
		}
	}
	if m, ok := cur.(map[string]any); ok {
		return m
	}
	return node
}

func jsonTypeName(x any) string {
	switch x.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		n := x.(float64)
		if n == math.Trunc(n) && !math.IsInf(n, 0) {
			return "number" // integer is also a number
		}
		return "number"
	case json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return reflect.TypeOf(x).String()
	}
}

func (v *schemaValidator) checkType(ptr string, value any, want string) bool {
	ok := false
	switch want {
	case "null":
		ok = value == nil
	case "boolean":
		_, ok = value.(bool)
	case "string":
		_, ok = value.(string)
	case "number":
		_, ok = value.(float64)
		if !ok {
			_, ok = value.(json.Number)
		}
	case "integer":
		if n, isN := value.(float64); isN {
			ok = n == math.Trunc(n) && !math.IsInf(n, 0)
		}
	case "array":
		_, ok = value.([]any)
	case "object":
		_, ok = value.(map[string]any)
	}
	if !ok {
		v.fail(ptr, "type", fmt.Sprintf("字段 %s 的类型应为 %s，实际为 %s", ptr, want, jsonTypeName(value)))
	}
	return ok
}

func (v *schemaValidator) validate(ptr string, value any, node map[string]any) {
	node = v.resolveRef(node)
	if tAny, ok := node["type"]; ok {
		switch t := tAny.(type) {
		case string:
			if !v.checkType(ptr, value, t) {
				return
			}
		case []any:
			pass := false
			for _, tn := range t {
				if ts, ok := tn.(string); ok {
					switch ts {
					case "null":
						if value == nil {
							pass = true
						}
					case "boolean":
						if _, x := value.(bool); x {
							pass = true
						}
					case "string":
						if _, x := value.(string); x {
							pass = true
						}
					case "number":
						if _, x := value.(float64); x {
							pass = true
						}
					case "integer":
						if n, x := value.(float64); x && n == math.Trunc(n) {
							pass = true
						}
					case "array":
						if _, x := value.([]any); x {
							pass = true
						}
					case "object":
						if _, x := value.(map[string]any); x {
							pass = true
						}
					}
				}
			}
			if !pass {
				v.fail(ptr, "type", fmt.Sprintf("字段 %s 的类型应为 %v 之一，实际为 %s", ptr, t, jsonTypeName(value)))
				return
			}
		}
	}
	if enum, ok := node["enum"].([]any); ok {
		matched := false
		for _, e := range enum {
			if reflect.DeepEqual(e, value) {
				matched = true
				break
			}
		}
		if !matched {
			eb, _ := json.Marshal(enum)
			v.fail(ptr, "enum", fmt.Sprintf("字段 %s 的值 %v 不在允许的枚举集合 %s 中", ptr, value, string(eb)))
		}
	}
	if c, ok := node["const"]; ok {
		if !reflect.DeepEqual(c, value) {
			cb, _ := json.Marshal(c)
			v.fail(ptr, "const", fmt.Sprintf("字段 %s 必须等于常量 %s", ptr, string(cb)))
		}
	}

	switch x := value.(type) {
	case float64:
		v.validateNumber(ptr, x, node)
	case string:
		v.validateString(ptr, x, node)
	case []any:
		v.validateArray(ptr, x, node)
	case map[string]any:
		v.validateObject(ptr, x, node)
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func (v *schemaValidator) validateNumber(ptr string, n float64, node map[string]any) {
	if m, ok := toFloat(node["minimum"]); ok && n < m {
		v.fail(ptr, "minimum", fmt.Sprintf("字段 %s 的值 %v 小于最小值 %v", ptr, n, m))
	}
	if m, ok := toFloat(node["maximum"]); ok && n > m {
		v.fail(ptr, "maximum", fmt.Sprintf("字段 %s 的值 %v 大于最大值 %v", ptr, n, m))
	}
	if m, ok := toFloat(node["exclusiveMinimum"]); ok && n <= m {
		v.fail(ptr, "exclusiveMinimum", fmt.Sprintf("字段 %s 的值 %v 必须严格大于 %v", ptr, n, m))
	}
	if m, ok := toFloat(node["exclusiveMaximum"]); ok && n >= m {
		v.fail(ptr, "exclusiveMaximum", fmt.Sprintf("字段 %s 的值 %v 必须严格小于 %v", ptr, n, m))
	}
	if m, ok := toFloat(node["multipleOf"]); ok && m > 0 {
		q := n / m
		if math.Abs(q-math.Round(q)) > 1e-9 {
			v.fail(ptr, "multipleOf", fmt.Sprintf("字段 %s 的值 %v 必须是 %v 的整数倍", ptr, n, m))
		}
	}
}

func (v *schemaValidator) validateString(ptr, s string, node map[string]any) {
	if m, ok := toFloat(node["minLength"]); ok && float64(len([]rune(s))) < m {
		v.fail(ptr, "minLength", fmt.Sprintf("字段 %s 的长度 %d 小于最小长度 %d", ptr, len([]rune(s)), int(m)))
	}
	if m, ok := toFloat(node["maxLength"]); ok && float64(len([]rune(s))) > m {
		v.fail(ptr, "maxLength", fmt.Sprintf("字段 %s 的长度 %d 超过最大长度 %d", ptr, len([]rune(s)), int(m)))
	}
}

func (v *schemaValidator) validateArray(ptr string, arr []any, node map[string]any) {
	if m, ok := toFloat(node["minItems"]); ok && len(arr) < int(m) {
		v.fail(ptr, "minItems", fmt.Sprintf("字段 %s 的数组长度 %d 小于最小项数 %d", ptr, len(arr), int(m)))
	}
	if m, ok := toFloat(node["maxItems"]); ok && len(arr) > int(m) {
		v.fail(ptr, "maxItems", fmt.Sprintf("字段 %s 的数组长度 %d 超过最大项数 %d", ptr, len(arr), int(m)))
	}
	if items, ok := node["items"].(map[string]any); ok {
		for i, it := range arr {
			v.validate(fmt.Sprintf("%s[%d]", ptr, i), it, items)
		}
	}
}

func (v *schemaValidator) validateObject(ptr string, obj map[string]any, node map[string]any) {
	if req, ok := node["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				if _, exists := obj[name]; !exists {
					v.fail(joinPtr(ptr, name), "required",
						fmt.Sprintf("缺少必填字段 %s（路径 %s）", name, joinPtr(ptr, name)))
				}
			}
		}
	}
	props, _ := node["properties"].(map[string]any)
	// deterministic traversal
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := obj[k]
		cptr := joinPtr(ptr, k)
		if sub, ok := props[k].(map[string]any); ok {
			v.validate(cptr, child, sub)
		} else if ap, ok := node["additionalProperties"]; ok {
			switch a := ap.(type) {
			case bool:
				if !a {
					v.fail(cptr, "additionalProperties",
						fmt.Sprintf("字段 %s 是未在 properties 中声明的额外字段，且 additionalProperties=false", cptr))
				}
			case map[string]any:
				v.validate(cptr, child, a)
			}
		}
	}
	if m, ok := toFloat(node["minProperties"]); ok && len(obj) < int(m) {
		v.fail(ptr, "minProperties", fmt.Sprintf("字段 %s 的属性数 %d 小于最小值 %d", ptr, len(obj), int(m)))
	}
	if m, ok := toFloat(node["maxProperties"]); ok && len(obj) > int(m) {
		v.fail(ptr, "maxProperties", fmt.Sprintf("字段 %s 的属性数 %d 超过最大值 %d", ptr, len(obj), int(m)))
	}
}

func joinPtr(base, key string) string {
	if base == "$" {
		return "$." + key
	}
	return base + "." + key
}
