// Package rule parses and executes the default router rule template. It is a
// leaf package so that both config validation and the builder can use it.
package rule

import (
	"strings"
	"text/template"
	"unicode"
)

// RuleModel is the data the default rule template is executed with.
type RuleModel struct {
	Name          string
	ContainerName string
	Labels        map[string]string
}

var funcs = template.FuncMap{
	"normalize":  Normalize,
	"lower":      strings.ToLower,
	"upper":      strings.ToUpper,
	"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
	"trimSuffix": func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
	"replace":    func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
}

// Parse parses the default rule template tpl. It fails on syntax errors and on
// unknown functions.
func Parse(tpl string) (*template.Template, error) {
	return template.New("defaultRule").Funcs(funcs).Parse(tpl)
}

// Execute renders t with model.
func Execute(t *template.Template, model RuleModel) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, model); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Normalize replaces every run of characters that are neither letters nor digits
// with a single "-" and trims leading and trailing separators.
func Normalize(name string) string {
	return strings.Join(strings.FieldsFunc(name, notLetterOrDigit), "-")
}

func notLetterOrDigit(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}
