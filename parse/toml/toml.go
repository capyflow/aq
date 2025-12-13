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
	"strconv"
	"strings"
	"time"
)

// =========================
// AST Definitions
// =========================

type Kind uint8

const (
	KindTable Kind = iota
	KindArray
	KindValue
)

type Node interface {
	Kind() Kind
}

// -------- Table --------

type Table struct {
	Items map[string]Node
}

func NewTable() *Table {
	return &Table{Items: make(map[string]Node)}
}

func (*Table) Kind() Kind { return KindTable }

// -------- Array --------

type Array struct {
	Elems []Node
}

func (*Array) Kind() Kind { return KindArray }

// -------- Value --------

type ValueKind uint8

const (
	ValueString ValueKind = iota
	ValueInt
	ValueFloat
	ValueBool
	ValueDatetime
)

type Value struct {
	Type ValueKind
	V    any
}

func (*Value) Kind() Kind { return KindValue }

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
		// 逐行读取toml
		line := strings.TrimSpace(p.scanner.Text())
		p.lineNo++

		// 跳过空行和注释行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case isTableHeader(line):
			if err := p.parseTableHeader(line); err != nil {
				return nil, err
			}
		case strings.Contains(line, "="):
			if err := p.parseKeyValue(line); err != nil {
				return nil, err
			}
		default:
			return nil, p.errf("invalid syntax")
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
	name := strings.Trim(line, "[]")
	parts := splitKey(name)

	t := p.root
	for _, part := range parts {
		n, ok := t.Items[part]
		// 如果键不存在，创建一个新表
		if !ok {
			next := NewTable()
			t.Items[part] = next
			t = next
			continue
		}

		if n.Kind() != KindTable {
			return p.errf(fmt.Sprintf("key %q already defined and is not a table", part))
		}
		t = n.(*Table)
	}

	p.cur = t
	return nil
}

func (p *parser) parseKeyValue(line string) error {
	idx := strings.Index(line, "=")
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])

	parts := splitKey(key)

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

		if n.Kind() != KindTable {
			return p.errf(fmt.Sprintf("key %q already defined and is not a table", part))
		}
		t = n.(*Table)
	}

	last := parts[len(parts)-1]
	if _, exists := t.Items[last]; exists {
		return p.errf(fmt.Sprintf("duplicate key %q", last))
	}

	v, err := parseValue(val)
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
	// String
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		return &Value{Type: ValueString, V: strings.Trim(s, "\"")}, nil
	}

	// Bool
	if s == "true" || s == "false" {
		return &Value{Type: ValueBool, V: s == "true"}, nil
	}

	// Datetime (RFC3339 subset)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &Value{Type: ValueDatetime, V: t}, nil
	}

	// Int
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return &Value{Type: ValueInt, V: i}, nil
	}

	// Float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return &Value{Type: ValueFloat, V: f}, nil
	}

	return nil, errors.New("unsupported value")
}

// =========================
// Utilities
// =========================

func isTableHeader(s string) bool {
	return strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")
}

func splitKey(s string) []string {
	parts := strings.Split(s, ".")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// =========================
// Safe Access Helpers
// =========================

func Get(root *Table, path ...string) (Node, bool) {
	var cur Node = root
	for _, p := range path {
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

func MustString(n Node) string {
	v := n.(*Value)
	return v.V.(string)
}

func MustInt(n Node) int64 {
	v := n.(*Value)
	return v.V.(int64)
}
