// Package merge implements the three-layer configuration merge kernel:
// public -> namespace -> group, with format-specific semantics and
// per-key provenance tracking.
package merge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"configplat/internal/model"
)

// LayerDoc is one layer's parsed document plus its format.
type LayerDoc struct {
	Layer  string         // model.LayerPublic / LayerNamespace / LayerGroup
	Format string         // format shared across the three layers for one key
	Doc    map[string]any // parsed value
	Raw    string         // original text (for key-level layers)
}

// KeyResult is the merged result for one configuration key.
type KeyResult struct {
	Key        string         `json:"key"`
	Format     string         `json:"format"`
	Source     string         `json:"source"` // public | namespace | group
	Merged     map[string]any `json:"merged"`
	Rendered   string         `json:"rendered"`
	Sources    map[string]string `json:"sources,omitempty"` // deep path -> layer (deep merge only)
}

// Result holds the merged configuration for one group in one environment.
type Result struct {
	// Keys lists every effective key, each with provenance.
	Keys []KeyResult `json:"keys"`
	// Rendered is the final merged document keyed by config key.
	Rendered map[string]string `json:"rendered"`
}

// Merge merges the three layers for a single configuration key.
// Layers may carry nil Doc when the layer does not define the key.
// Semantics:
//   - JSON / YAML: recursive deep merge of nested objects; arrays/scalars
//     are replaced by the lower layer's value.
//   - Properties / TOML: top-level key-level override; nested structures in
//     TOML tables also merge per top-level key (the whole table is replaced
//     when the lower layer defines that table key).
func Merge(key string, public, namespace, group *LayerDoc) KeyResult {
	format := pickFormat(public, namespace, group)
	res := KeyResult{Key: key, Format: format, Sources: map[string]string{}}

	var source string
	var merged map[string]any

	switch format {
	case model.FormatJSON, model.FormatYAML:
		merged, source, res.Sources = deepMergeLayers(public, namespace, group)
	case model.FormatProperties:
		merged, source = shallowMergeLayers(public, namespace, group)
	case model.FormatTOML:
		merged, source = shallowMergeLayers(public, namespace, group)
	default:
		merged = map[string]any{}
	}
	res.Merged = merged
	res.Source = source
	res.Rendered = Render(format, merged)
	return res
}

func pickFormat(layers ...*LayerDoc) string {
	for _, l := range layers {
		if l != nil && l.Format != "" {
			return l.Format
		}
	}
	return model.FormatJSON
}

// deepMergeLayers overlays the three layers recursively. It returns the merged
// document, the "dominant" source (layer that defined the root key) and a
// JSON-pointer-like path -> layer provenance map.
func deepMergeLayers(public, namespace, group *LayerDoc) (map[string]any, string, map[string]string) {
	prov := map[string]string{}
	merged := map[string]any{}
	rootSource := ""
	if public != nil && public.Doc != nil {
		overlayDeep(merged, public.Doc, prov, "$", model.LayerPublic)
		rootSource = model.LayerPublic
	}
	if namespace != nil && namespace.Doc != nil {
		overlayDeep(merged, namespace.Doc, prov, "$", model.LayerNamespace)
		rootSource = model.LayerNamespace
	}
	if group != nil && group.Doc != nil {
		overlayDeep(merged, group.Doc, prov, "$", model.LayerGroup)
		rootSource = model.LayerGroup
	}
	return merged, rootSource, prov
}

func childPath(base, key string) string {
	if base == "$" {
		return "$." + key
	}
	return base + "." + key
}

// removeSubtree deletes provenance entries strictly below path (used when a
// lower layer replaces an object with a scalar/array).
func removeSubtree(prov map[string]string, path string) {
	prefix := path + "."
	for p := range prov {
		if strings.HasPrefix(p, prefix) {
			delete(prov, p)
		}
	}
}

// overlayDeep merges src over dst in place:
//   - where both sides at a path are objects, merge field by field;
//   - everything else (scalar, array, null, object-over-scalar) is replaced;
//   - every path the overlay defines is marked with the layer, and stale
//     provenance underneath a replaced object is removed.
func overlayDeep(dst, src map[string]any, prov map[string]string, basePath, layer string) {
	for k, overlay := range src {
		p := childPath(base, k)
		existing, exists := dst[k]
		if overlayMap, ok := overlay.(map[string]any); ok && exists {
			if existingMap, ok2 := existing.(map[string]any); ok2 {
				prov[p] = layer
				overlayDeep(existingMap, overlayMap, prov, p, layer)
				continue
			}
		}
		if _, isObj := overlay.(map[string]any); !isObj {
			removeSubtree(prov, p)
		}
		dst[k] = clone(overlay)
		markPaths(prov, p, overlay, layer)
	}
}

// shallowMergeLayers overlays top-level keys; the lower layer wins per key.
// TOML: nested tables are values keyed by their top-level table name.
func shallowMergeLayers(public, namespace, group *LayerDoc) (map[string]any, string) {
	merged := map[string]any{}
	rootSource := ""
	for _, l := range []*LayerDoc{public, namespace, group} {
		if l == nil || l.Doc == nil {
			continue
		}
		for k, v := range l.Doc {
			merged[k] = v
		}
		rootSource = l.Layer
	}
	return merged, rootSource
}

func markPaths(dst map[string]string, path string, val any, layer string) {
	dst[path] = layer
	if m, ok := val.(map[string]any); ok {
		for k, v := range m {
			markPaths(dst, path+"."+k, v, layer)
		}
	}
}

// MergeGroup combines all keys visible to a group.
// layersByKey[key] = {public doc, namespace doc, group doc} (any may be nil).
func MergeGroup(entries map[string][3]*LayerDoc) Result {
	out := Result{Rendered: map[string]string{}}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ls := entries[k]
		kr := Merge(k, ls[0], ls[1], ls[2])
		out.Keys = append(out.Keys, kr)
		out.Rendered[k] = kr.Rendered
	}
	return out
}

// Render serializes a merged document back to the declared format.
func Render(format string, doc map[string]any) string {
	if doc == nil {
		doc = map[string]any{}
	}
	switch format {
	case model.FormatJSON:
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return ""
		}
		return string(b)
	case model.FormatYAML:
		b, err := yaml.Marshal(doc)
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(b), "\n")
	case model.FormatProperties:
		return renderProperties(doc)
	case model.FormatTOML:
		return renderTOML(doc)
	default:
		return ""
	}
}

func renderProperties(doc map[string]any) string {
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := doc[k]
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%v", scalarToText(v)))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func scalarToText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// renderTOML renders a flat or one-level-nested map: scalar keys first,
// then [tables]. Deeply nested tables are rendered with dotted headers.
func renderTOML(doc map[string]any) string {
	var scalars []string
	var tables []string
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := doc[k].(type) {
		case map[string]any:
			tables = append(tables, renderTOMLTable(k, v))
		case []any:
			tables = append(tables, renderTOMLArray(k, v))
		default:
			scalars = append(scalars, fmt.Sprintf("%s = %s", tomlKey(k), tomlScalar(v)))
		}
	}
	parts := []string{strings.Join(scalars, "\n")}
	parts = append(parts, tables...)
	out := strings.Join(nonEmpty(parts), "\n\n")
	return out
}

func nonEmpty(parts []string) []string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

func renderTOMLTable(header string, m map[string]any) string {
	var lines []string
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := m[k].(type) {
		case map[string]any:
			lines = append(lines, renderTOMLTable(header+"."+k, v))
		case []any:
			lines = append(lines, renderTOMLArray(header+"."+k, v))
		default:
			lines = append(lines, fmt.Sprintf("%s = %s", tomlKey(k), tomlScalar(v)))
		}
	}
	// only emit the header for this table's own scalar lines; nested tables
	// already carry their own headers via recursion above.
	var own []string
	for _, k := range keys {
		if _, ok := m[k].(map[string]any); ok {
			continue
		}
		if _, ok := m[k].([]any); ok {
			continue
		}
		own = append(own, fmt.Sprintf("%s = %s", tomlKey(k), tomlScalar(m[k])))
	}
	var nested []string
	for _, l := range lines {
		if strings.HasPrefix(l, "[") {
			nested = append(nested, l)
		}
	}
	head := ""
	if len(own) > 0 {
		head = fmt.Sprintf("[%s]\n%s", header, strings.Join(own, "\n"))
	}
	all := append([]string{head}, nested...)
	return strings.Join(nonEmpty(all), "\n\n")
}

func renderTOMLArray(key string, arr []any) string {
	var b strings.Builder
	for _, item := range arr {
		b.WriteString(fmt.Sprintf("[[%s]]\n", key))
		if m, ok := item.(map[string]any); ok {
			ks := make([]string, 0, len(m))
			for k := range m {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				b.WriteString(fmt.Sprintf("%s = %s\n", tomlKey(k), tomlScalar(m[k])))
			}
		} else {
			b.WriteString(fmt.Sprintf("%s\n", tomlScalar(item)))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func tomlKey(k string) string {
	if strings.ContainsAny(k, " .\t=\"'") {
		return fmt.Sprintf("%q", k)
	}
	return k
}

func tomlScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return `""`
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
