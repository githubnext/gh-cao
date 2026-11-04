package adxtrace

import (
	"strings"
	"testing"
	"time"
)

func TestApplyBudgetDefaultsFillsOnlyUnsetBudgets(t *testing.T) {
	c := Config{MaxEntries: 7}
	c.applyBudgetDefaults()
	if c.MaxTraceBytes != 8<<20 || c.MaxSpoolBytes != 256<<20 || c.MaxEntries != 7 ||
		c.Retention != 7*24*time.Hour || c.FrameDelay != 24*time.Hour || c.MiningInterval != 5*time.Minute {
		t.Fatalf("unexpected budgets: %+v", c)
	}
}

func TestValidateAppliesDefaultsToEnabledConfig(t *testing.T) {
	c := configFor(t, "full")
	if c.MaxTraceBytes != 8<<20 || c.Retention != 7*24*time.Hour {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestPolicyValidateRejectsUnapprovedOrMalformedPolicies(t *testing.T) {
	valid := func() Policy {
		return Policy{Version: "v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"user.message": {"content"}}}
	}
	if err := valid().validate(); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Policy){
		"content not approved": func(p *Policy) { p.ContentApproved = false },
		"rights not approved":  func(p *Policy) { p.RightsApproved = false },
		"missing version":      func(p *Policy) { p.Version = "" },
		"empty events":         func(p *Policy) { p.Events = nil },
		"bad event name":       func(p *Policy) { p.Events = map[string][]string{"1bad": {"content"}} },
		"bad field name":       func(p *Policy) { p.Events = map[string][]string{"user.message": {"bad field"}} },
		"too many patterns":    func(p *Policy) { p.RedactPatterns = make([]string, 33) },
		"large pattern":        func(p *Policy) { p.RedactPatterns = []string{strings.Repeat("a", 4097)} },
		"invalid pattern":      func(p *Policy) { p.RedactPatterns = []string{"("} },
	}
	for name, mutate := range tests {
		p := valid()
		mutate(&p)
		if err := p.validate(); err == nil {
			t.Errorf("%s: policy admitted", name)
		}
	}
}
