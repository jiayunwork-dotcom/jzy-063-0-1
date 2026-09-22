// Package validate implements syntax validation for the four supported
// configuration formats and optional JSON Schema validation.
package validate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"configplat/internal/model"
)

// FieldError names the exact JSON pointer / field and violated constraint.
type FieldError struct {
	Field      string `json:"field"`
	Constraint string `json:"constraint"`
	Message    string `json:"message"`
}

// Result reports why a value was rejected.
type Result struct {
	Valid      bool         `json:"valid"`
	Format     string       `json:"format"`
	Line       int          `json:"line,omitempty"`
	Column     int          `json:"column,omitempty"`
	SyntaxMsg  string       `json:"syntaxMsg,omitempty"`
	FieldError *FieldError  `json:"fieldError,omitempty"`
	Errors     []FieldError `json:"errors,omitempty"`
}

func syntaxError(format string, line, col int, msg string) Result {
	return Result{Valid: false, Format: format, Line: line, Column: col, SyntaxMsg: msg}
}

// Check validates raw text of the given format, then optionally validates the
// parsed document against the supplied JSON Schema. Empty values are valid
// (treated as an empty document).
func Check(format, value, schema string) Result {
	if !model.SupportedFormats[format] {
		return syntaxError(format, 0, 0, fmt.Sprintf("不支持的格式: %s", format))
	}
	doc, res := parse(format, value)
	if !res.Valid {
		return res
	}
	if strings.TrimSpace(schema) != "" {
		if ferr := checkSchema(schema, doc, format); ferr != nil {
			return Result{Valid: false, Format: format, FieldError: &ferr[0], Errors: ferr}
		}
	}
	return Result{Valid: true, Format: format}
}

// Parse parses raw text of the given format into a generic document:
// JSON / YAML / TOML decode to map[string]any; Properties to a flat
// map[string]string wrapped as map[string]any.
func Parse(format, value string) (map[string]any, error) {
	doc, res := parse(format, value)
	if !res.Valid {
		return nil, fmt.Errorf("%s 语法错误 (第 %d 行): %s", format, res.Line, res.SyntaxMsg)
	}
	return doc, nil
}

func parse(format, value string) (map[string]any, Result) {
	if strings.TrimSpace(value) == "" {
		return map[string]any{}, Result{Valid: true, Format: format}
	}
	switch format {
	case model.FormatJSON:
		return parseJSON(value)
	case model.FormatYAML:
		return parseYAML(value)
	case model.FormatProperties:
		return parseProperties(value)
	case model.FormatTOML:
		return parseTOML(value)
	default:
		return nil, syntaxError(format, 0, 0, "不支持的格式: "+format)
	}
}

func parseJSON(value string) (map[string]any, Result) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(value), &doc); err != nil {
		line, col := locateJSON([]byte(value), err)
		return nil, syntaxError(model.FormatJSON, line, col, err.Error())
	}
	return doc, Result{Valid: true, Format: model.FormatJSON}
}

// locateJSON recovers a best-effort 1-based line/column for a json error.
func locateJSON(data []byte, err error) (int, int) {
	if se, ok := err.(*json.SyntaxError); ok {
		off := int(se.Offset)
		line, col := 1, 1
		for i := 0; i < off-1 && i < len(data); i++ {
			if data[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
		}
		return line, col
	}
	return 0, 0
}

func parseYAML(value string) (map[string]any, Result) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(value), &doc); err != nil {
		line := scanYAMLErrorLine(value, err.Error())
		return nil, syntaxError(model.FormatYAML, line, 0, cleanYAMLError(err.Error()))
	}
	return doc, Result{Valid: true, Format: model.FormatYAML}
}

func scanYAMLErrorLine(raw, msg string) int {
	const marker = "line "
	if i := strings.Index(msg, marker); i >= 0 {
		var n int
		_, err := fmt.Sscanf(msg[i+len(marker):], "%d", &n)
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func cleanYAMLError(msg string) string {
	msg = strings.TrimPrefix(msg, "yaml: ")
	// Turn the common "cannot unmarshal X into Go value of type ..." into
	// something an operator understands.
	if strings.Contains(msg, "cannot unmarshal") {
		return "顶层必须是一个对象（键值映射），不能是数组或标量: " + msg
	}
	return msg
}

func parseTOML(value string) (map[string]any, Result) {
	var doc map[string]any
	if err := toml.Unmarshal([]byte(value), &doc); err != nil {
		line, col := extractTOMLPosition(err.Error())
		return nil, syntaxError(model.FormatTOML, line, col, cleanTOMLError(err.Error()))
	}
	return doc, Result{Valid: true, Format: model.FormatTOML}
}

func extractTOMLPosition(msg string) (int, int) {
	// go-toml v2 errors look like: "toml: expected a bare key but got '=' at line 2 column 5"
	var line, col int
	if i := strings.Index(msg, "at line "); i >= 0 {
		rest := msg[i+len("at line "):]
		if _, err := fmt.Sscanf(rest, "%d column %d", &line, &col); err == nil {
			return line, col
		}
	}
	if i := strings.Index(msg, "line "); i >= 0 {
		if _, err := fmt.Sscanf(msg[i:], "line %d column %d", &line, &col); err == nil {
			return line, col
		}
	}
	return 0, 0
}

func cleanTOMLError(msg string) string {
	msg = strings.TrimPrefix(msg, "toml: ")
	return msg
}
