package policy

import "testing"

func TestAllowsModel_EmptyAllowsAll(t *testing.T) {
	p := &Policy{}
	if !p.AllowsModel("anything") {
		t.Error("expected empty AllowedModels to allow any model")
	}
}

func TestAllowsModel_Match(t *testing.T) {
	p := &Policy{AllowedModels: []string{"a", "b", "c"}}
	for _, m := range []string{"a", "b", "c"} {
		if !p.AllowsModel(m) {
			t.Errorf("expected %s to be allowed", m)
		}
	}
}

func TestAllowsModel_Reject(t *testing.T) {
	p := &Policy{AllowedModels: []string{"a", "b"}}
	if p.AllowsModel("c") {
		t.Error("expected c to be rejected")
	}
}
