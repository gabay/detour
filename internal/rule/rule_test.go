package rule

import "testing"

func TestParseAndExecute(t *testing.T) {
	model := RuleModel{Name: "My_App.web", ContainerName: "/my-app", Labels: map[string]string{"host": "a.io"}}
	tests := []struct {
		name    string
		tpl     string
		want    string
		wantErr bool
	}{
		{name: "normalize", tpl: "Host(`{{ normalize .Name }}`)", want: "Host(`My-App-web`)"},
		{name: "lower and upper", tpl: "{{ lower .Name }}{{ upper .Name }}", want: "my_app.webMY_APP.WEB"},
		{name: "trimPrefix", tpl: "{{ trimPrefix \"/\" .ContainerName }}", want: "my-app"},
		{name: "trimSuffix pipeline", tpl: "{{ .ContainerName | trimSuffix \"app\" }}", want: "/my-"},
		{name: "replace pipeline", tpl: "{{ .Name | replace \"_\" \"-\" }}", want: "My-App.web"},
		{name: "label access", tpl: "Host(`{{ index .Labels \"host\" }}`)", want: "Host(`a.io`)"},
		{name: "syntax error", tpl: "Host(`{{ normalize .Name`)", wantErr: true},
		{name: "unknown function", tpl: "{{ sprig_upper .Name }}", wantErr: true},
		{name: "unknown field at execution", tpl: "{{ .Missing }}", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl, err := Parse(tt.tpl)
			if err == nil {
				var got string
				got, err = Execute(tpl, model)
				if err == nil && got != tt.want {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
		})
	}
}

func TestTrimProject(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"project container", "project_container", "container"},
		{"stack service name", "stack_service_name", "name"},
		{"last underscore wins", "a_b_c", "c"},
		{"no underscore", "plain", "plain"},
		{"trailing underscore unchanged", "name_", "name_"},
		{"leading underscore", "_x", "x"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TrimProject(tt.in); got != tt.want {
				t.Errorf("TrimProject(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTrimProjectInTemplate(t *testing.T) {
	tpl, err := Parse("Host(`{{ trimProject .Name | normalize }}.example.com`)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Execute(tpl, RuleModel{Name: "myproj_web_app"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "Host(`app.example.com`)"; got != want {
		t.Errorf("Execute = %q, want %q", got, want)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"--a_b..c--": "a-b-c", "plain": "plain", "x  y": "x-y"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
