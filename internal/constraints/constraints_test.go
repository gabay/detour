package constraints

import "testing"

func TestMatch(t *testing.T) {
	labels := map[string]string{"traefik.constraint": "public", "env": "prod-eu"}
	tests := []struct {
		name    string
		expr    string
		want    bool
		wantErr bool
	}{
		{name: "empty matches", expr: "", want: true},
		{name: "label equal", expr: `Label("traefik.constraint", "public")`, want: true},
		{name: "label differs", expr: `Label("traefik.constraint", "internal")`, want: false},
		{name: "label missing", expr: `Label("missing", "x")`, want: false},
		{name: "regex match", expr: `LabelRegex("env", "^prod")`, want: true},
		{name: "regex no match", expr: `LabelRegex("env", "^dev")`, want: false},
		{name: "regex invalid is false", expr: `LabelRegex("env", "([")`, want: false},
		{name: "regex invalid negated is true", expr: `!LabelRegex("env", "([")`, want: true},
		{name: "and", expr: `Label("traefik.constraint", "public") && LabelRegex("env", "prod")`, want: true},
		{name: "and false", expr: `Label("traefik.constraint", "public") && Label("env", "x")`, want: false},
		{name: "or", expr: `Label("env", "x") || Label("traefik.constraint", "public")`, want: true},
		{name: "not", expr: `!Label("env", "x")`, want: true},
		{name: "parentheses", expr: `!(Label("env", "prod-eu") || Label("x", "y")) || Label("traefik.constraint", "public")`, want: true},
		{name: "syntax error", expr: `Label("env"`, wantErr: true},
		{name: "unknown function", expr: `Foo("env")`, wantErr: true},
		{name: "not boolean", expr: `"text"`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Match(labels, tt.expr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Match() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr bool
	}{
		{expr: ""},
		{expr: `Label("a", "b") && !LabelRegex("c", "d")`},
		{expr: `Label("a", "b") &&`, wantErr: true},
		{expr: `Nope()`, wantErr: true},
	}
	for _, tt := range tests {
		if err := Validate(tt.expr); (err != nil) != tt.wantErr {
			t.Errorf("Validate(%q) = %v, wantErr %v", tt.expr, err, tt.wantErr)
		}
	}
}
