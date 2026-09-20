package pypi

import (
	"fmt"
	"strconv"
	"strings"
)

// Environment is the fixed target environment used to evaluate PEP 508
// markers. Requirements that do not apply to it (Windows-only packages, old
// Python back-ports) are skipped instead of failing the run.
type Environment struct {
	PythonVersion      string // "3.12"
	PythonFullVersion  string // "3.12.3"
	SysPlatform        string // "linux"
	PlatformSystem     string // "Linux"
	PlatformMachine    string // "x86_64"
	OSName             string // "posix"
	ImplementationName string
	Extras             []string
}

// DefaultEnvironment describes Ubuntu 24.04 / CPython 3.12 / amd64.
func DefaultEnvironment(t Target) Environment {
	py := fmt.Sprintf("%d.%d", t.PyMajor, t.PyMinor)
	return Environment{
		PythonVersion:      py,
		PythonFullVersion:  py + ".0",
		SysPlatform:        "linux",
		PlatformSystem:     "Linux",
		PlatformMachine:    t.Arch,
		OSName:             "posix",
		ImplementationName: "cpython",
	}
}

func (e Environment) lookup(name string) (string, bool) {
	switch name {
	case "python_version":
		return e.PythonVersion, true
	case "python_full_version":
		return e.PythonFullVersion, true
	case "sys_platform":
		return e.SysPlatform, true
	case "platform_system":
		return e.PlatformSystem, true
	case "platform_machine":
		return e.PlatformMachine, true
	case "os_name":
		return e.OSName, true
	case "implementation_name":
		return e.ImplementationName, true
	case "platform_python_implementation", "python_implementation":
		return "CPython", true
	case "platform_release", "platform_version":
		return "", true
	}
	return "", false
}

// EvalMarker evaluates a PEP 508 marker expression. Anything it cannot
// understand evaluates to true, so an unusual marker never silently drops a
// package the user asked for.
func EvalMarker(expr string, env Environment) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}
	p := &markerParser{toks: tokenizeMarker(expr), env: env}
	v, err := p.parseOr()
	if err != nil || p.pos != len(p.toks) {
		return true
	}
	return v
}

type markerParser struct {
	toks []string
	pos  int
	env  Environment
}

func tokenizeMarker(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(' || c == ')':
			out = append(out, string(c))
			i++
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			out = append(out, strconv.Quote(s[i+1:min(j, len(s))]))
			i = min(j+1, len(s))
		case strings.ContainsRune("=!<>~", rune(c)):
			j := i
			for j < len(s) && strings.ContainsRune("=!<>~", rune(s[j])) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t()=!<>~'\"", rune(s[j])) {
				j++
			}
			if j == i {
				j++
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

func (p *markerParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *markerParser) next() string {
	t := p.peek()
	p.pos++
	return t
}

func (p *markerParser) parseOr() (bool, error) {
	v, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for strings.EqualFold(p.peek(), "or") {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		v = v || r
	}
	return v, nil
}

func (p *markerParser) parseAnd() (bool, error) {
	v, err := p.parseTerm()
	if err != nil {
		return false, err
	}
	for strings.EqualFold(p.peek(), "and") {
		p.next()
		r, err := p.parseTerm()
		if err != nil {
			return false, err
		}
		v = v && r
	}
	return v, nil
}

func (p *markerParser) parseTerm() (bool, error) {
	if p.peek() == "(" {
		p.next()
		v, err := p.parseOr()
		if err != nil {
			return false, err
		}
		if p.next() != ")" {
			return false, fmt.Errorf("unbalanced parenthesis")
		}
		return v, nil
	}
	left := p.next()
	op := p.next()
	if strings.EqualFold(op, "not") && strings.EqualFold(p.peek(), "in") {
		p.next()
		op = "not in"
	} else if strings.EqualFold(op, "in") {
		op = "in"
	}
	right := p.next()
	if left == "" || op == "" || right == "" {
		return false, fmt.Errorf("incomplete marker")
	}
	return p.compare(left, op, right)
}

func (p *markerParser) compare(left, op, right string) (bool, error) {
	lv, lIsVar := p.value(left)
	rv, rIsVar := p.value(right)
	if !lIsVar && !rIsVar {
		return true, fmt.Errorf("no variable in comparison")
	}
	// "extra" comparisons: keep a requirement only when the extra was asked
	// for; airgapkit mirrors every extra it is told about, so treat unknown
	// extras as requested.
	if left == "extra" || right == "extra" {
		return true, nil
	}
	switch op {
	case "in":
		return strings.Contains(rv, lv), nil
	case "not in":
		return !strings.Contains(rv, lv), nil
	}
	if isVersionVar(left) || isVersionVar(right) {
		spec := op + rv
		if isVersionVar(right) {
			spec = flipOp(op) + lv
			return MatchSpecifier(spec, rv), nil
		}
		return MatchSpecifier(spec, lv), nil
	}
	switch op {
	case "==", "===":
		return lv == rv, nil
	case "!=":
		return lv != rv, nil
	case "<":
		return lv < rv, nil
	case "<=":
		return lv <= rv, nil
	case ">":
		return lv > rv, nil
	case ">=":
		return lv >= rv, nil
	}
	return true, nil
}

func isVersionVar(name string) bool {
	return name == "python_version" || name == "python_full_version" || name == "platform_release"
}

func flipOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}

// value resolves a token to its string value; the boolean reports whether the
// token was an environment variable rather than a literal.
func (p *markerParser) value(tok string) (string, bool) {
	if unq, err := strconv.Unquote(tok); err == nil {
		return unq, false
	}
	if v, ok := p.env.lookup(tok); ok {
		return v, true
	}
	return tok, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
