package validate

import (
	"fmt"
	"strings"
)

// parseProperties implements a Java-properties style reader:
//   - lines of the form key=value or key:value (whitespace separator also ok)
//   - '#' / '!' start a comment, blank lines ignored
//   - a trailing backslash continues the value on the next line
//   - duplicate keys, invalid escapes and lines that are neither
//     key=value/key:value nor blank/comment are syntax errors
func parseProperties(value string) (map[string]any, Result) {
	doc := map[string]any{}
	seen := map[string]int{}
	lines := strings.Split(value, "\n")

	var pendingKey, pendingVal string
	continuing := false
	pendingStart := 0

	finish := func(key, val string, startLine int) *Result {
		key = strings.TrimSpace(key)
		if key == "" {
			r := syntaxError("properties", startLine, 0, "配置项键不能为空")
			return &r
		}
		if ln, ok := seen[key]; ok {
			r := syntaxError("properties", startLine, 0,
				fmt.Sprintf("键 %q 重复定义（首次出现在第 %d 行）", key, ln))
			return &r
		}
		seen[key] = startLine
		unescaped, err := unescapeProps(strings.TrimSpace(val))
		if err != nil {
			r := syntaxError("properties", startLine, 0, err.Error())
			return &r
		}
		doc[key] = unescaped
		return nil
	}

	for idx, raw := range lines {
		lineNo := idx + 1
		trimmed := strings.TrimLeft(raw, " \t")

		if !continuing {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
				continue
			}
			key, val, found := splitPropsEntry(trimmed)
			if !found {
				return nil, syntaxError("properties", lineNo, 0,
					fmt.Sprintf("第 %d 行格式非法，应为 key=value 或 key:value: %q", lineNo, trimmed))
			}
			if strings.HasSuffix(val, "\\") && !(len(val) >= 2 && val[len(val)-2] == '\\') {
				pendingKey, pendingVal = key, val[:len(val)-1]
				continuing, pendingStart = true, lineNo
				continue
			}
			if r := finish(key, val, lineNo); r != nil {
				return nil, *r
			}
		} else {
			if strings.HasSuffix(trimmed, "\\") && !(len(trimmed) >= 2 && trimmed[len(trimmed)-2] == '\\') {
				pendingVal += "\n" + trimmed[:len(trimmed)-1]
				continue
			}
			pendingVal += "\n" + trimmed
			if r := finish(pendingKey, pendingVal, pendingStart); r != nil {
				return nil, *r
			}
			continuing = false
		}
	}
	if continuing {
		if r := finish(pendingKey, pendingVal, pendingStart); r != nil {
			return nil, *r
		}
	}
	return doc, Result{Valid: true, Format: "properties"}
}

// splitPropsEntry splits "key = value" style text. The first unescaped '=' or
// ':' wins; otherwise the first whitespace run before a value.
func splitPropsEntry(line string) (key, val string, found bool) {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' {
			i++
			continue
		}
		if c == '=' || c == ':' {
			return line[:i], line[i+1:], true
		}
		if c == ' ' || c == '\t' {
			j := i
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j < len(line) {
				return line[:i], line[j:], true
			}
			return line[:i], "", true // bare key -> empty value
		}
	}
	return strings.TrimSpace(line), "", true
}

func unescapeProps(s string) (string, error) {
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("非法转义: 行尾单独的反斜杠")
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '=':
			b.WriteByte('=')
		case ':':
			b.WriteByte(':')
		case 'u':
			if i+4 >= len(s) {
				return "", fmt.Errorf("非法 unicode 转义 \\u，需要 4 位十六进制数字")
			}
			var r rune
			for j := 1; j <= 4; j++ {
				c := s[i+j]
				var d int
				switch {
				case c >= '0' && c <= '9':
					d = int(c - '0')
				case c >= 'a' && c <= 'f':
					d = int(c-'a') + 10
				case c >= 'A' && c <= 'F':
					d = int(c-'A') + 10
				default:
					return "", fmt.Errorf("非法 unicode 转义: %q 不是十六进制数字", string(c))
				}
				r = r*16 + rune(d)
			}
			b.WriteRune(r)
			i += 4
		default:
			return "", fmt.Errorf("非法转义序列: \\%q", string(s[i]))
		}
	}
	return b.String(), nil
}
