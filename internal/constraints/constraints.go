// Package constraints evaluates Traefik-style constraint expressions, such as
// Label("traefik.constraint", "public") && !LabelRegex("env", "^dev"), against
// container labels.
package constraints

import (
	"fmt"
	"regexp"

	"github.com/vulcand/predicate"
)

// matcher is the value a parsed expression evaluates to.
type matcher func(labels map[string]string) bool

var parser, parserErr = predicate.NewParser(predicate.Def{
	Operators: predicate.Operators{
		AND: and,
		OR:  or,
		NOT: not,
	},
	Functions: map[string]interface{}{
		"Label":      label,
		"LabelRegex": labelRegex,
	},
})

// Validate reports whether expr is a well-formed constraint expression. An
// empty expression is valid and matches every container.
func Validate(expr string) error {
	_, err := parse(expr)
	return err
}

// Match reports whether labels satisfy expr. An empty expression matches
// everything. Malformed expressions return an error.
func Match(labels map[string]string, expr string) (bool, error) {
	m, err := parse(expr)
	if err != nil {
		return false, err
	}
	if m == nil {
		return true, nil
	}
	return m(labels), nil
}

func parse(expr string) (matcher, error) {
	if expr == "" {
		return nil, nil
	}
	if parserErr != nil {
		return nil, parserErr
	}
	v, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid constraint %q: %w", expr, err)
	}
	m, ok := v.(matcher)
	if !ok {
		return nil, fmt.Errorf("invalid constraint %q: not a boolean expression", expr)
	}
	return m, nil
}

func and(a, b matcher) matcher {
	return func(labels map[string]string) bool { return a(labels) && b(labels) }
}

func or(a, b matcher) matcher {
	return func(labels map[string]string) bool { return a(labels) || b(labels) }
}

func not(a matcher) matcher {
	return func(labels map[string]string) bool { return !a(labels) }
}

func label(name, value string) matcher {
	return func(labels map[string]string) bool {
		v, ok := labels[name]
		return ok && v == value
	}
}

// labelRegex matches when the label is present and its value matches the
// regular expression. An invalid expression never matches.
func labelRegex(name, expr string) matcher {
	re, err := regexp.Compile(expr)
	if err != nil {
		return func(map[string]string) bool { return false }
	}
	return func(labels map[string]string) bool {
		v, ok := labels[name]
		return ok && re.MatchString(v)
	}
}
