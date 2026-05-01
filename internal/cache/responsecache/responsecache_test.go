package responsecache

import "testing"

func TestKey_DifferentTenantsAreIsolated(t *testing.T) {
	a := Key("tenant-a", "llama3.2", "hello")
	b := Key("tenant-b", "llama3.2", "hello")
	if a == b {
		t.Errorf("expected different keys per tenant, got identical: %s", a)
	}
}

func TestKey_DifferentPromptsHashDifferently(t *testing.T) {
	a := Key("t1", "llama3.2", "hello")
	b := Key("t1", "llama3.2", "world")
	if a == b {
		t.Errorf("expected different keys for different prompts")
	}
}

func TestKey_DifferentModelsHashDifferently(t *testing.T) {
	a := Key("t1", "llama3.2", "hello")
	b := Key("t1", "mistral", "hello")
	if a == b {
		t.Errorf("expected different keys for different models")
	}
}

func TestKey_Stable(t *testing.T) {
	a := Key("t1", "llama3.2", "hello")
	b := Key("t1", "llama3.2", "hello")
	if a != b {
		t.Errorf("expected stable key, got %s vs %s", a, b)
	}
}

func TestKey_Format(t *testing.T) {
	got := Key("t1", "llama3.2", "hello")
	const wantPrefix = "cache:t1:"
	if len(got) <= len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Errorf("expected prefix %q, got %q", wantPrefix, got)
	}
	// 64 hex chars after prefix
	if len(got)-len(wantPrefix) != 64 {
		t.Errorf("expected 64 hex chars after prefix, got %d", len(got)-len(wantPrefix))
	}
}
