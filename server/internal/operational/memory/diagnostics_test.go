package memory

import "testing"

func TestLoadCounterIndex(t *testing.T) {
	cases := []struct {
		name      string
		wantIndex int
		wantOK    bool
	}{
		{"webhookReceived", 0, true},
		{"collectionSucceeded", 1, true},
		{"collectionFailed", 2, true},
		{"webhookDuplicate", -1, false},
		{"taskQueued", -1, false},
		{"", -1, false},
	}
	for _, c := range cases {
		index, ok := loadCounterIndex(c.name)
		if index != c.wantIndex || ok != c.wantOK {
			t.Errorf("loadCounterIndex(%q) = (%d, %t), want (%d, %t)", c.name, index, ok, c.wantIndex, c.wantOK)
		}
	}
}

func TestHealthEventIndex(t *testing.T) {
	cases := []struct {
		event, code string
		wantIndex   int
		wantOK      bool
	}{
		{"failure", "admission", 0, true},
		{"failure", "collection", 0, true},
		{"failure", "operational", 0, true},
		{"failure", "", -1, false},
		{"failure", "unknown", -1, false},
		{"success", "", 2, true},
		{"success", "unexpected", -1, false},
		{"webhook", "", 3, true},
		{"webhook", "unexpected", -1, false},
		{"unknown", "", -1, false},
		{"", "", -1, false},
	}
	for _, c := range cases {
		index, ok := healthEventIndex(c.event, c.code)
		if index != c.wantIndex || ok != c.wantOK {
			t.Errorf("healthEventIndex(%q, %q) = (%d, %t), want (%d, %t)",
				c.event, c.code, index, ok, c.wantIndex, c.wantOK)
		}
	}
}
