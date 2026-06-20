package schema

import (
	"encoding/json"
	"testing"
)

// The schema contract: keys are always present, missing values are explicit nulls.
func TestToolMarshalsAllKeysWithNulls(t *testing.T) {
	b, err := json.Marshal(Unavailable(ToolCodex))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := []string{
		"tool", "available", "error", "stale", "collected_at", "backend",
		"plan", "model", "session", "limits", "credits", "fallback",
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			t.Errorf("key %q missing from marshaled Tool", k)
			continue
		}
		switch k {
		case "tool", "available", "stale", "backend":
			// non-nullable
		default:
			if string(v) != "null" {
				t.Errorf("key %q = %s, want null for unavailable tool", k, v)
			}
		}
	}
	if string(m["backend"]) != `"unknown"` {
		t.Errorf("backend = %s, want \"unknown\"", m["backend"])
	}
}

// model.effort follows the same key-always-present / explicit-null contract:
// a nil Effort must marshal to "effort": null, never be omitted.
func TestModelEffortMarshalsNull(t *testing.T) {
	b, err := json.Marshal(Model{ID: "claude-opus-4-8"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["effort"]
	if !ok {
		t.Fatal(`key "effort" missing from marshaled Model`)
	}
	if string(v) != "null" {
		t.Errorf(`effort = %s, want null`, v)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	pct := 5.0
	mins := 300
	resets := "2026-06-13T02:00:58+09:00"
	s := Status{
		SchemaVersion: Version,
		GeneratedAt:   "2026-06-12T21:00:00+09:00",
		Tools: []Tool{{
			Tool:      ToolCodex,
			Available: true,
			Backend:   BackendSubscription,
			Limits: []Limit{{
				Window:        WindowFiveHour,
				WindowMinutes: &mins,
				UsedPct:       &pct,
				ResetsAt:      &resets,
			}},
		}},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Status
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != Version || len(got.Tools) != 1 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	l := got.Tools[0].Limits[0]
	if l.Window != WindowFiveHour || *l.UsedPct != 5.0 || *l.ResetsAt != resets {
		t.Errorf("limit round trip mismatch: %+v", l)
	}
}
