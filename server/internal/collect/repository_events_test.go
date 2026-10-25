package collect

import "testing"

func TestRepositoryLifecycleEvent(t *testing.T) {
	for _, test := range []struct {
		action string
		state  string
	}{
		{"created", "active"}, {"archived", "archived"},
		{"unarchived", "active"}, {"deleted", "deleted"},
		{"renamed", "active"}, {"transferred", "active"},
	} {
		t.Run(test.action, func(t *testing.T) {
			intent, err := ParseEvent("repository", []byte(`{"action":"`+test.action+`","repository":{"id":42,"full_name":"Octo/API"},"installation":{"id":7}}`))
			if err != nil {
				t.Fatal(err)
			}
			if intent.Kind != IntentRepository || intent.Repository != "octo/api" ||
				intent.RepositoryID != 42 || intent.InstallationID != 7 || intent.Lifecycle != test.state {
				t.Fatalf("unexpected lifecycle intent: %+v", intent)
			}
		})
	}
	for _, payload := range []string{
		`{"action":"deleted","repository":{"id":42,"full_name":"octo/api"}}`,
		`{"action":"deleted","repository":{"full_name":"octo/api"},"installation":{"id":7}}`,
		`{"action":"deleted","repository":{"id":42,"full_name":"../api"},"installation":{"id":7}}`,
		`{"action":"edited","repository":{"id":42,"full_name":"octo/api"},"installation":{"id":7}}`,
	} {
		intent, err := ParseEvent("repository", []byte(payload))
		if err != nil || intent.Kind != IntentIgnore {
			t.Fatalf("accepted invalid repository event: %+v, %v", intent, err)
		}
	}
}
