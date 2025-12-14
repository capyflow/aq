package toml

// toml 包实现了一个生产级的 TOML 解析器，具有强大的内部 AST、确定性语义和安全的解析后操作。
//
// 范围：
// - TOML v1.0.0 核心功能
// - 显式 AST（表 / 数组 / 值）
// - 安全的点分键处理
// - 表扩展语义
// - 确定性错误
//
// 非目标（设计如此）：
// - 注释保留
// - 格式化往返
// - 流式突变
//
// 此实现适用于生产环境，作为配置摄取层。

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// =========================
// AST Definitions
// =========================

type ValueKind string

var tomlValueKinds = struct {
	ValueString        ValueKind
	ValueInt           ValueKind
	ValueFloat         ValueKind
	ValueBool          ValueKind
	ValueDatetime      ValueKind
	ValueLocalDate     ValueKind
	ValueLocalTime     ValueKind
	ValueLocalDatetime ValueKind
	ValueTable         ValueKind
	ValueArray         ValueKind
}{
	ValueString:        "string",
	ValueInt:           "int",
	ValueFloat:         "float",
	ValueBool:          "bool",
	ValueDatetime:      "datetime",
	ValueLocalDate:     "local_date",
	ValueLocalTime:     "local_time",
	ValueLocalDatetime: "local_datetime",
	ValueTable:         "table",
	ValueArray:         "array",
}

type Node interface {
	Kind() ValueKind
	Value() any
}

// -------- Table --------

type Table struct {
	Items map[string]Node
}

func NewTable() *Table {
	return &Table{Items: make(map[string]Node)}
}

func (*Table) Kind() ValueKind { return tomlValueKinds.ValueTable }

func (*Table) Value() any { return nil }

// -------- Array --------

type Array struct {
	Elems []Node
}

func (v *Array) Kind() ValueKind { return tomlValueKinds.ValueArray }

func (v *Array) Value() any { return v.Elems }

// -------- Value --------

type Value struct {
	Type ValueKind
	V    any
}

func (v *Value) Kind() ValueKind { return v.Type }

func (v *Value) Value() any { return v.V }

// =========================
// Public API
// =========================

// Parse parses TOML input from r and returns a root Table.
func Parse(r io.Reader) (*Table, error) {
	p := &parser{
		scanner: bufio.NewScanner(r),
		root:    NewTable(),
		cur:     nil,
	}
	p.cur = p.root

	for p.scanner.Scan() {
		line := strings.TrimSpace(p.scanner.Text())
		p.lineNo++

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case strings.HasPrefix(line, "["):
			if err := p.parseTableHeader(line); err != nil {
				return nil, err
			}
		default:
			idx := findUnquotedEqual(line)
			if idx < 0 {
				return nil, p.errf("invalid syntax")
			}
			if err := p.parseKeyValue(line, idx); err != nil {
				return nil, err
			}
		}
	}

	if err := p.scanner.Err(); err != nil {
		return nil, err
	}

	return p.root, nil
}

// =========================
// Parser Implementation
// =========================

type parser struct {
	scanner *bufio.Scanner
	root    *Table
	cur     *Table
	lineNo  int
}

func (p *parser) parseTableHeader(line string) error {
	s := stripCommentPreserveStrings(line)
	s = strings.TrimSpace(s)
	isArray := strings.HasPrefix(s, "[[")
	if isArray {
		if !strings.HasSuffix(s, "]]") {
			return p.errf("invalid array-of-table header")
		}
	} else {
		if !strings.HasSuffix(s, "]") {
			return p.errf("invalid table header")
		}
	}
	var name string
	if isArray {
		name = strings.TrimSpace(s[2 : len(s)-2])
	} else {
		name = strings.TrimSpace(s[1 : len(s)-1])
	}
	parts, err := parseKeyParts(name)
	if err != nil {
		return p.errf(err.Error())
	}

	if !isArray {
		t := p.root
		for _, part := range parts {
			n, ok := t.Items[part]
			if !ok {
				next := NewTable()
				t.Items[part] = next
				t = next
				continue
			}

			if n.Kind() != tomlValueKinds.ValueTable {
				return p.errf(fmt.Sprintf("key %q already defined and is not a table", part))
			}
			t = n.(*Table)
		}

		p.cur = t
		return nil
	}

	parent := p.root
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		n, ok := parent.Items[part]
		if !ok {
			next := NewTable()
			parent.Items[part] = next
			parent = next
			continue
		}
		if n.Kind() != tomlValueKinds.ValueTable {
			return p.errf(fmt.Sprintf("key %q already defined and is not a table", part))
		}
		parent = n.(*Table)
	}
	last := parts[len(parts)-1]
	existing, ok := parent.Items[last]
	var arr *Array
	if !ok {
		arr = &Array{Elems: make([]Node, 0)}
		parent.Items[last] = arr
	} else {
		if existing.Kind() != tomlValueKinds.ValueArray {
			return p.errf(fmt.Sprintf("key %q already defined and is not an array", last))
		}
		arr = existing.(*Array)
	}
	newTbl := NewTable()
	arr.Elems = append(arr.Elems, newTbl)
	p.cur = newTbl
	return nil
}

func (p *parser) parseKeyValue(line string, idx int) error {
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])

	parts, err := parseKeyParts(key)
	if err != nil {
		return p.errf(err.Error())
	}

	t := p.cur
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		n, ok := t.Items[part]
		if !ok {
			next := NewTable()
			t.Items[part] = next
			t = next
			continue
		}

		if n.Kind() != tomlValueKinds.ValueTable {
			return p.errf(fmt.Sprintf("key %q already defined and is not a table", part))
		}
		t = n.(*Table)
	}

	last := parts[len(parts)-1]
	if _, exists := t.Items[last]; exists {
		return p.errf(fmt.Sprintf("duplicate key %q", last))
	}

	fullVal, err := p.consumeValue(val)
	if err != nil {
		return p.errf(err.Error())
	}
	v, err := parseValue(fullVal)
	if err != nil {
		return p.errf(err.Error())
	}

	t.Items[last] = v
	return nil
}

func (p *parser) errf(msg string) error {
	return fmt.Errorf("toml:%d: %s", p.lineNo, msg)
}

// =========================
// Value Parsing
// =========================

func parseValue(s string) (Node, error) {
	s = strings.TrimSpace(stripCommentPreserveStrings(s))
	if s == "" {
		return nil, errors.New("empty value")
	}
	if strings.HasPrefix(s, `"""`) {
		content, ok := extractTripleQuoted(s, '"')
		if !ok {
			return nil, errors.New("unterminated multiline string")
		}
		decoded, err := decodeBasicString(content, true)
		if err != nil {
			return nil, err
		}
		return &Value{Type: tomlValueKinds.ValueString, V: decoded}, nil
	}
	if strings.HasPrefix(s, `'''`) {
		content, ok := extractTripleQuoted(s, '\'')
		if !ok {
			return nil, errors.New("unterminated multiline literal string")
		}
		return &Value{Type: tomlValueKinds.ValueString, V: content}, nil
	}
	if strings.HasPrefix(s, `"`) {
		content, ok := extractSingleQuoted(s, '"')
		if !ok {
			return nil, errors.New("unterminated string")
		}
		decoded, err := decodeBasicString(content, false)
		if err != nil {
			return nil, err
		}
		return &Value{Type: tomlValueKinds.ValueString, V: decoded}, nil
	}
	if strings.HasPrefix(s, `'`) {
		content, ok := extractSingleQuoted(s, '\'')
		if !ok {
			return nil, errors.New("unterminated literal string")
		}
		return &Value{Type: tomlValueKinds.ValueString, V: content}, nil
	}
	if strings.HasPrefix(s, "[") {
		arr, err := parseArrayToken(s)
		if err != nil {
			return nil, err
		}
		return arr, nil
	}
	if strings.HasPrefix(s, "{") {
		tbl, err := parseInlineTableToken(s)
		if err != nil {
			return nil, err
		}
		return tbl, nil
	}
	if s == "true" || s == "false" {
		return &Value{Type: tomlValueKinds.ValueBool, V: s == "true"}, nil
	}
	if s == "inf" || s == "+inf" {
		return &Value{Type: tomlValueKinds.ValueFloat, V: math.Inf(+1)}, nil
	}
	if s == "-inf" {
		return &Value{Type: tomlValueKinds.ValueFloat, V: math.Inf(-1)}, nil
	}
	if strings.EqualFold(s, "nan") {
		return &Value{Type: tomlValueKinds.ValueFloat, V: math.NaN()}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return &Value{Type: tomlValueKinds.ValueDatetime, V: t}, nil
	}
	if t, ok := parseLocalDateTimeVariants(s); ok {
		return t, nil
	}
	if i, ok := parseIntToken(s); ok == nil {
		return &Value{Type: tomlValueKinds.ValueInt, V: i}, nil
	}
	if f, ok := parseFloatToken(s); ok == nil {
		return &Value{Type: tomlValueKinds.ValueFloat, V: f}, nil
	}
	return nil, errors.New("unsupported value")
}

// =========================
// Utilities
// =========================

func parseKeyParts(s string) ([]string, error) {
	var parts []string
	var cur strings.Builder
	inQuote := byte(0)
	escape := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote != 0 {
			if inQuote == '"' && ch == '\\' && !escape {
				escape = true
				continue
			}
			if escape {
				cur.WriteByte(ch)
				escape = false
				continue
			}
			if ch == inQuote {
				inQuote = 0
				continue
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' || ch == '\'' {
			if cur.Len() != 0 && strings.TrimSpace(cur.String()) != "" {
				return nil, errors.New("invalid quoted key position")
			}
			inQuote = ch
			cur.Reset()
			continue
		}
		if ch == '.' {
			part := strings.TrimSpace(cur.String())
			if part != "" {
				parts = append(parts, part)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if inQuote != 0 {
		return nil, errors.New("unterminated quoted key")
	}
	last := strings.TrimSpace(cur.String())
	if last != "" {
		parts = append(parts, last)
	}
	return parts, nil
}

func stripCommentPreserveStrings(s string) string {
	var b strings.Builder
	inBasic := false
	inLiteral := false
	basicMulti := false
	literalMulti := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inBasic {
			if ch == '\\' {
				if i+1 < len(s) {
					b.WriteByte(ch)
					i++
					b.WriteByte(s[i])
					continue
				}
			}
			if basicMulti {
				if i+2 < len(s) && s[i] == '"' && s[i+1] == '"' && s[i+2] == '"' {
					inBasic = false
					basicMulti = false
					b.WriteString(`"""`)
					i += 2
					continue
				}
			} else {
				if ch == '"' {
					inBasic = false
				}
			}
			b.WriteByte(ch)
			continue
		}
		if inLiteral {
			if literalMulti {
				if i+2 < len(s) && s[i] == '\'' && s[i+1] == '\'' && s[i+2] == '\'' {
					inLiteral = false
					literalMulti = false
					b.WriteString(`'''`)
					i += 2
					continue
				}
			} else {
				if ch == '\'' {
					inLiteral = false
				}
			}
			b.WriteByte(ch)
			continue
		}
		if ch == '"' {
			if i+2 < len(s) && s[i+1] == '"' && s[i+2] == '"' {
				inBasic = true
				basicMulti = true
				b.WriteString(`"""`)
				i += 2
			} else {
				inBasic = true
				basicMulti = false
				b.WriteByte(ch)
			}
			continue
		}
		if ch == '\'' {
			if i+2 < len(s) && s[i+1] == '\'' && s[i+2] == '\'' {
				inLiteral = true
				literalMulti = true
				b.WriteString(`'''`)
				i += 2
			} else {
				inLiteral = true
				literalMulti = false
				b.WriteByte(ch)
			}
			continue
		}
		if ch == '#' {
			break
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func findUnquotedEqual(s string) int {
	inBasic := false
	inLiteral := false
	basicMulti := false
	literalMulti := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inBasic {
			if ch == '\\' {
				i++
				continue
			}
			if basicMulti {
				if i+2 < len(s) && s[i] == '"' && s[i+1] == '"' && s[i+2] == '"' {
					inBasic = false
					basicMulti = false
					i += 2
					continue
				}
			} else if ch == '"' {
				inBasic = false
				continue
			}
			continue
		}
		if inLiteral {
			if literalMulti {
				if i+2 < len(s) && s[i] == '\'' && s[i+1] == '\'' && s[i+2] == '\'' {
					inLiteral = false
					literalMulti = false
					i += 2
					continue
				}
			} else if ch == '\'' {
				inLiteral = false
				continue
			}
			continue
		}
		if ch == '"' {
			if i+2 < len(s) && s[i+1] == '"' && s[i+2] == '"' {
				inBasic = true
				basicMulti = true
				i += 2
			} else {
				inBasic = true
			}
			continue
		}
		if ch == '\'' {
			if i+2 < len(s) && s[i+1] == '\'' && s[i+2] == '\'' {
				inLiteral = true
				literalMulti = true
				i += 2
			} else {
				inLiteral = true
			}
			continue
		}
		if ch == '=' {
			return i
		}
	}
	return -1
}

func (p *parser) consumeValue(initial string) (string, error) {
	s := initial
	sTrim := strings.TrimSpace(stripCommentPreserveStrings(s))
	if sTrim == "" {
		return "", errors.New("empty value")
	}
	if strings.HasPrefix(sTrim, `"""`) && !strings.Contains(sTrim[3:], `"""`) {
		var b strings.Builder
		b.WriteString(s)
		for {
			if !p.scanner.Scan() {
				return "", errors.New("unterminated multiline string")
			}
			line := p.scanner.Text()
			p.lineNo++
			b.WriteString("\n")
			b.WriteString(line)
			text := b.String()
			if strings.Contains(text[len(initial):], `"""`) {
				break
			}
		}
		return b.String(), nil
	}
	if strings.HasPrefix(sTrim, `'''`) && !strings.Contains(sTrim[3:], `'''`) {
		var b strings.Builder
		b.WriteString(s)
		for {
			if !p.scanner.Scan() {
				return "", errors.New("unterminated multiline literal string")
			}
			line := p.scanner.Text()
			p.lineNo++
			b.WriteString("\n")
			b.WriteString(line)
			text := b.String()
			if strings.Contains(text[len(initial):], `'''`) {
				break
			}
		}
		return b.String(), nil
	}
	if strings.HasPrefix(sTrim, "[") || strings.HasPrefix(sTrim, "{") {
		var b strings.Builder
		b.WriteString(s)
		depth := 0
		inBasic := false
		inLiteral := false
		basicMulti := false
		literalMulti := false
		for i := 0; i < len(s); i++ {
			ch := s[i]
			if inBasic {
				if ch == '\\' {
					i++
					continue
				}
				if basicMulti {
					if i+2 < len(s) && s[i] == '"' && s[i+1] == '"' && s[i+2] == '"' {
						inBasic = false
						basicMulti = false
						i += 2
						continue
					}
				} else if ch == '"' {
					inBasic = false
					continue
				}
				continue
			}
			if inLiteral {
				if literalMulti {
					if i+2 < len(s) && s[i] == '\'' && s[i+1] == '\'' && s[i+2] == '\'' {
						inLiteral = false
						literalMulti = false
						i += 2
						continue
					}
				} else if ch == '\'' {
					inLiteral = false
					continue
				}
				continue
			}
			if ch == '"' {
				if i+2 < len(s) && s[i+1] == '"' && s[i+2] == '"' {
					inBasic = true
					basicMulti = true
					i += 2
				} else {
					inBasic = true
				}
				continue
			}
			if ch == '\'' {
				if i+2 < len(s) && s[i+1] == '\'' && s[i+2] == '\'' {
					inLiteral = true
					literalMulti = true
					i += 2
				} else {
					inLiteral = true
				}
				continue
			}
			if ch == '[' || ch == '{' {
				depth++
			} else if ch == ']' || ch == '}' {
				depth--
			}
		}
		for depth > 0 {
			if !p.scanner.Scan() {
				return "", errors.New("unterminated compound value")
			}
			line := p.scanner.Text()
			p.lineNo++
			b.WriteString("\n")
			b.WriteString(line)
			s = line
			for i := 0; i < len(s); i++ {
				ch := s[i]
				if inBasic {
					if ch == '\\' {
						i++
						continue
					}
					if basicMulti {
						if i+2 < len(s) && s[i] == '"' && s[i+1] == '"' && s[i+2] == '"' {
							inBasic = false
							basicMulti = false
							i += 2
							continue
						}
					} else if ch == '"' {
						inBasic = false
						continue
					}
					continue
				}
				if inLiteral {
					if literalMulti {
						if i+2 < len(s) && s[i] == '\'' && s[i+1] == '\'' && s[i+2] == '\'' {
							inLiteral = false
							literalMulti = false
							i += 2
							continue
						}
					} else if ch == '\'' {
						inLiteral = false
						continue
					}
					continue
				}
				if ch == '"' {
					if i+2 < len(s) && s[i+1] == '"' && s[i+2] == '"' {
						inBasic = true
						basicMulti = true
						i += 2
					} else {
						inBasic = true
					}
					continue
				}
				if ch == '\'' {
					if i+2 < len(s) && s[i+1] == '\'' && s[i+2] == '\'' {
						inLiteral = true
						literalMulti = true
						i += 2
					} else {
						inLiteral = true
					}
					continue
				}
				if ch == '[' || ch == '{' {
					depth++
				} else if ch == ']' || ch == '}' {
					depth--
				}
			}
		}
		return b.String(), nil
	}
	return s, nil
}

func extractTripleQuoted(s string, quote byte) (string, bool) {
	if len(s) < 6 {
		return "", false
	}
	if quote == '"' && !strings.HasPrefix(s, `"""`) {
		return "", false
	}
	if quote == '\'' && !strings.HasPrefix(s, `'''`) {
		return "", false
	}
	end := `"""`
	if quote == '\'' {
		end = `'''`
	}
	idx := strings.Index(s[3:], end)
	if idx < 0 {
		return "", false
	}
	content := s[3 : 3+idx]
	if len(content) > 0 && content[0] == '\n' {
		content = content[1:]
	}
	return content, true
}

func extractSingleQuoted(s string, quote byte) (string, bool) {
	if len(s) < 2 || s[0] != quote || s[len(s)-1] != quote {
		return "", false
	}
	return s[1 : len(s)-1], true
}

func decodeBasicString(s string, multiline bool) (string, error) {
	if multiline {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '\\' {
				if i+1 < len(s) && s[i+1] == '\n' {
					i += 1
					for i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
						i++
					}
					continue
				}
			}
			b.WriteByte(s[i])
		}
		s = b.String()
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '\\' {
			out.WriteByte(ch)
			continue
		}
		if i+1 >= len(s) {
			return "", errors.New("invalid escape")
		}
		i++
		switch s[i] {
		case 'b':
			out.WriteByte('\b')
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'f':
			out.WriteByte('\f')
		case 'r':
			out.WriteByte('\r')
		case '"':
			out.WriteByte('"')
		case '\\':
			out.WriteByte('\\')
		case 'u':
			if i+4 >= len(s) {
				return "", errors.New("invalid unicode escape")
			}
			h := s[i+1 : i+5]
			r, err := parseHexRune(h)
			if err != nil {
				return "", err
			}
			out.WriteRune(r)
			i += 4
		case 'U':
			if i+8 >= len(s) {
				return "", errors.New("invalid unicode escape")
			}
			h := s[i+1 : i+9]
			r, err := parseHexRune(h)
			if err != nil {
				return "", err
			}
			out.WriteRune(r)
			i += 8
		default:
			return "", errors.New("unsupported escape")
		}
	}
	return out.String(), nil
}

func parseHexRune(h string) (rune, error) {
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, err
	}
	return rune(v), nil
}

func parseArrayToken(s string) (*Array, error) {
	content := strings.TrimSpace(stripCommentPreserveStrings(s))
	if !strings.HasPrefix(content, "[") {
		return nil, errors.New("invalid array")
	}
	content = strings.TrimSpace(content[1 : len(content)-1])
	parts := splitTopLevel(content, ',')
	arr := &Array{Elems: make([]Node, 0, len(parts))}
	var elemKind ValueKind
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		v, err := parseValue(part)
		if err != nil {
			return nil, err
		}
		if len(arr.Elems) == 0 {
			elemKind = v.Kind()
		} else {
			if v.Kind() != elemKind {
				return nil, errors.New("mixed-type array")
			}
		}
		arr.Elems = append(arr.Elems, v)
	}
	return arr, nil
}

func parseInlineTableToken(s string) (*Table, error) {
	content := strings.TrimSpace(stripCommentPreserveStrings(s))
	if !strings.HasPrefix(content, "{") || !strings.HasSuffix(content, "}") {
		return nil, errors.New("invalid inline table")
	}
	inner := strings.TrimSpace(content[1 : len(content)-1])
	pairs := splitTopLevel(inner, ',')
	t := NewTable()
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := findUnquotedEqual(pair)
		if idx < 0 {
			return nil, errors.New("invalid inline table kv")
		}
		key := strings.TrimSpace(pair[:idx])
		val := strings.TrimSpace(pair[idx+1:])
		parts, err := parseKeyParts(key)
		if err != nil {
			return nil, err
		}
		cur := t
		for i := 0; i < len(parts)-1; i++ {
			part := parts[i]
			n, ok := cur.Items[part]
			if !ok {
				next := NewTable()
				cur.Items[part] = next
				cur = next
				continue
			}
			if n.Kind() != tomlValueKinds.ValueTable {
				return nil, errors.New("inline table path conflict")
			}
			cur = n.(*Table)
		}
		last := parts[len(parts)-1]
		if _, exists := cur.Items[last]; exists {
			return nil, errors.New("duplicate inline table key")
		}
		v, err := parseValue(val)
		if err != nil {
			return nil, err
		}
		cur.Items[last] = v
	}
	return t, nil
}

func splitTopLevel(s string, sep rune) []string {
	var parts []string
	var cur strings.Builder
	depthB := 0
	depthC := 0
	inBasic := false
	inLiteral := false
	basicMulti := false
	literalMulti := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inBasic {
			if ch == '\\' {
				i++
				if i < len(s) {
					cur.WriteByte('\\')
					cur.WriteByte(s[i])
				}
				continue
			}
			if basicMulti {
				if i+2 < len(s) && s[i] == '"' && s[i+1] == '"' && s[i+2] == '"' {
					inBasic = false
					basicMulti = false
					cur.WriteString(`"""`)
					i += 2
					continue
				}
			} else if ch == '"' {
				inBasic = false
			}
			cur.WriteByte(ch)
			continue
		}
		if inLiteral {
			if literalMulti {
				if i+2 < len(s) && s[i] == '\'' && s[i+1] == '\'' && s[i+2] == '\'' {
					inLiteral = false
					literalMulti = false
					cur.WriteString(`'''`)
					i += 2
					continue
				}
			} else if ch == '\'' {
				inLiteral = false
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' {
			if i+2 < len(s) && s[i+1] == '"' && s[i+2] == '"' {
				inBasic = true
				basicMulti = true
				cur.WriteString(`"""`)
				i += 2
			} else {
				inBasic = true
				cur.WriteByte('"')
			}
			continue
		}
		if ch == '\'' {
			if i+2 < len(s) && s[i+1] == '\'' && s[i+2] == '\'' {
				inLiteral = true
				literalMulti = true
				cur.WriteString(`'''`)
				i += 2
			} else {
				inLiteral = true
				cur.WriteByte('\'')
			}
			continue
		}
		if ch == '[' {
			depthB++
			cur.WriteByte(ch)
			continue
		}
		if ch == ']' {
			depthB--
			cur.WriteByte(ch)
			continue
		}
		if ch == '{' {
			depthC++
			cur.WriteByte(ch)
			continue
		}
		if ch == '}' {
			depthC--
			cur.WriteByte(ch)
			continue
		}
		if depthB == 0 && depthC == 0 && rune(ch) == sep {
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		parts = append(parts, strings.TrimSpace(cur.String()))
	}
	return parts
}

func parseLocalDateTimeVariants(s string) (Node, bool) {
	layouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return &Value{Type: tomlValueKinds.ValueLocalDatetime, V: t}, true
		}
	}
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return &Value{Type: tomlValueKinds.ValueLocalDate, V: d}, true
	}
	timeLayouts := []string{
		"15:04:05",
		"15:04:05.999999999",
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return &Value{Type: tomlValueKinds.ValueLocalTime, V: t}, true
		}
	}
	return nil, false
}

func parseIntToken(s string) (int64, error) {
	s = strings.ReplaceAll(s, "_", "")
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "-0x") || strings.HasPrefix(s, "+0x") {
		sign := int64(1)
		if strings.HasPrefix(s, "-") {
			sign = -1
			s = s[1:]
		}
		if strings.HasPrefix(s, "+") {
			s = s[1:]
		}
		v, err := strconv.ParseUint(s[2:], 16, 64)
		if err != nil {
			return 0, err
		}
		return int64(v) * sign, nil
	}
	if strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "-0o") || strings.HasPrefix(s, "+0o") {
		sign := int64(1)
		if strings.HasPrefix(s, "-") {
			sign = -1
			s = s[1:]
		}
		if strings.HasPrefix(s, "+") {
			s = s[1:]
		}
		v, err := strconv.ParseUint(s[2:], 8, 64)
		if err != nil {
			return 0, err
		}
		return int64(v) * sign, nil
	}
	if strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "-0b") || strings.HasPrefix(s, "+0b") {
		sign := int64(1)
		if strings.HasPrefix(s, "-") {
			sign = -1
			s = s[1:]
		}
		if strings.HasPrefix(s, "+") {
			s = s[1:]
		}
		v, err := strconv.ParseUint(s[2:], 2, 64)
		if err != nil {
			return 0, err
		}
		return int64(v) * sign, nil
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return i, nil
}

func parseFloatToken(s string) (float64, error) {
	if s == "inf" || s == "+inf" {
		return math.Inf(+1), nil
	}
	if s == "-inf" {
		return math.Inf(-1), nil
	}
	if strings.EqualFold(s, "nan") {
		return math.NaN(), nil
	}
	s = strings.ReplaceAll(s, "_", "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

// =========================
// Safe Access Helpers
// =========================

func Get(root *Table, path ...string) (Node, bool) {
	var cur Node = root
	for _, p := range path {
		if len(p) == 0 {
			continue
		}
		t, ok := cur.(*Table)
		if !ok {
			return nil, false
		}
		cur, ok = t.Items[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func GetUntyped(root *Table, path ...string) (any, bool) {
	n, ok := Get(root, path...)
	if !ok {
		return nil, false
	}
	return ToUntyped(n), true
}

func ToUntyped(n Node) any {
	switch v := n.(type) {
	case *Value:
		return v.V
	case *Array:
		out := make([]any, len(v.Elems))
		for i := range v.Elems {
			out[i] = ToUntyped(v.Elems[i])
		}
		return out
	case *Table:
		m := make(map[string]any, len(v.Items))
		for k, child := range v.Items {
			m[k] = ToUntyped(child)
		}
		return m
	default:
		return nil
	}
}

func MustString(n Node) string {
	v := n.(*Value)
	return v.V.(string)
}

func MustInt(n Node) int64 {
	v := n.(*Value)
	return v.V.(int64)
}
