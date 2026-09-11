package config

import (
	"fmt"
	"strings"
)

// parseTOML parses the small subset of TOML that 0type's config file
// uses: comments, `[section]` headers, and `key = "value"` string
// assignments (double-quoted with \\, \", \n, \t escapes, or
// single-quoted literal strings). Values are returned as
// section -> key -> value, with top-level keys under the "" section.
//
// This is deliberately not a full TOML implementation, and it is not a
// lenient one either: anything outside the supported subset -- a number,
// a boolean, an array, an inline table, a multi-line string -- is a hard
// error naming the line, rather than being skipped or half-understood.
// The whole config surface is a handful of strings (a theme name and some
// shell commands), so a parser that can't silently misread a file is
// worth more here than one that accepts every valid TOML document, and it
// keeps 0type's dependency list at the one library it genuinely needs
// (ONNX Runtime). Files written to this subset are valid TOML, so a real
// TOML library could be swapped in later without breaking anyone's config.
func parseTOML(data []byte) (map[string]map[string]string, error) {
	out := map[string]map[string]string{"": {}}
	section := ""

	for i, raw := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: unterminated section header %q", lineNo, line)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" || strings.ContainsAny(name, "[]\"' \t") {
				return nil, fmt.Errorf("line %d: unsupported section header %q (want [name])", lineNo, line)
			}
			section = name
			if _, ok := out[section]; !ok {
				out[section] = map[string]string{}
			}
			continue
		}

		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected `key = \"value\"`, got %q", lineNo, line)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\"'[]") {
			return nil, fmt.Errorf("line %d: unsupported key %q", lineNo, key)
		}
		value, err := parseString(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("line %d: key %q: %w", lineNo, key, err)
		}
		if _, dup := out[section][key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", lineNo, key)
		}
		out[section][key] = value
	}

	return out, nil
}

// stripComment removes a trailing `#` comment, ignoring `#` inside a
// quoted string -- shell commands in hook values contain them often
// enough (`sh -c 'echo #done'`) that dropping everything after the first
// `#` would corrupt real configs.
func stripComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return line[:i]
		}
	}
	return line
}

// parseString unquotes a TOML basic (double-quoted, escapes honored) or
// literal (single-quoted, taken verbatim) string.
func parseString(s string) (string, error) {
	if len(s) < 2 {
		return "", fmt.Errorf("expected a quoted string, got %q", s)
	}

	switch s[0] {
	case '\'':
		if s[len(s)-1] != '\'' || strings.Contains(s[1:len(s)-1], "'") {
			return "", fmt.Errorf("unterminated literal string %q", s)
		}
		return s[1 : len(s)-1], nil
	case '"':
		var b strings.Builder
		escaped := false
		for i := 1; i < len(s); i++ {
			c := s[i]
			if escaped {
				switch c {
				case '\\', '"':
					b.WriteByte(c)
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					return "", fmt.Errorf("unsupported escape %q", `\`+string(c))
				}
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				if i != len(s)-1 {
					return "", fmt.Errorf("trailing characters after string: %q", s[i+1:])
				}
				return b.String(), nil
			default:
				b.WriteByte(c)
			}
		}
		return "", fmt.Errorf("unterminated string %q", s)
	default:
		return "", fmt.Errorf("expected a quoted string, got %q (only strings are supported)", s)
	}
}
