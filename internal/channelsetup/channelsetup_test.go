package channelsetup

import (
	"encoding/json"
	"testing"
)

func TestMergeManaged(t *testing.T) {
	cases := []struct {
		name     string
		existing string
	}{
		{name: "empty file", existing: ""},
		{name: "preserves unrelated keys", existing: `{"otherKey":"keep","permissions":{"allow":["Bash"]}}`},
		{name: "idempotent on already approved", existing: `{"channelsEnabled":true,"allowedChannelPlugins":[{"marketplace":"cc-review","plugin":"cc-review"}]}`},
		{name: "appends alongside another channel", existing: `{"allowedChannelPlugins":[{"marketplace":"claude-plugins-official","plugin":"discord"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := MergeManaged([]byte(tc.existing))
			if err != nil {
				t.Fatalf("MergeManaged: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(merged, &m); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if m["channelsEnabled"] != true {
				t.Errorf("channelsEnabled = %v, want true", m["channelsEnabled"])
			}
			has, err := ManagedHasEntry(merged)
			if err != nil {
				t.Fatalf("ManagedHasEntry: %v", err)
			}
			if !has {
				t.Errorf("merged file missing cc-review entry: %s", merged)
			}
			// No duplicate entries for cc-review, ever.
			list, _ := m["allowedChannelPlugins"].([]any)
			if got := countEntry(list, Marketplace, Plugin); got != 1 {
				t.Errorf("cc-review entry count = %d, want 1", got)
			}
		})
	}
}

func TestMergeManagedPreservesOtherKeys(t *testing.T) {
	merged, err := MergeManaged([]byte(`{"otherKey":"keep"}`))
	if err != nil {
		t.Fatalf("MergeManaged: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatal(err)
	}
	if m["otherKey"] != "keep" {
		t.Errorf("otherKey = %v, want keep", m["otherKey"])
	}
}

func TestMergeManagedIdempotent(t *testing.T) {
	once, err := MergeManaged(nil)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := MergeManaged(once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("not idempotent:\n once=%s\ntwice=%s", once, twice)
	}
}

func TestMergeManagedRejectsInvalidJSON(t *testing.T) {
	if _, err := MergeManaged([]byte("{not json")); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestMergeUserSettings(t *testing.T) {
	cases := []struct {
		name      string
		existing  string
		wantCount int
	}{
		{name: "empty file", existing: "", wantCount: 1},
		{name: "preserves other keys", existing: `{"theme":"dark"}`, wantCount: 1},
		{name: "dedupes existing channel", existing: `{"channels":["plugin:cc-review@cc-review"]}`, wantCount: 1},
		{name: "appends alongside another channel", existing: `{"channels":["plugin:discord@claude-plugins-official"]}`, wantCount: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := MergeUserSettings([]byte(tc.existing))
			if err != nil {
				t.Fatalf("MergeUserSettings: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(merged, &m); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			channels, _ := m["channels"].([]any)
			if len(channels) != tc.wantCount {
				t.Errorf("channels len = %d, want %d: %s", len(channels), tc.wantCount, merged)
			}
			if !containsString(channels, ChannelID) {
				t.Errorf("channels missing %s: %s", ChannelID, merged)
			}
		})
	}
}

func TestMergeUserSettingsPreservesOtherKeys(t *testing.T) {
	merged, err := MergeUserSettings([]byte(`{"theme":"dark"}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatal(err)
	}
	if m["theme"] != "dark" {
		t.Errorf("theme = %v, want dark", m["theme"])
	}
}

func countEntry(list []any, marketplace, plugin string) int {
	n := 0
	for _, e := range list {
		if obj, ok := e.(map[string]any); ok && obj["marketplace"] == marketplace && obj["plugin"] == plugin {
			n++
		}
	}
	return n
}
