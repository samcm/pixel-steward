package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseExample(t *testing.T) {
	t.Setenv("TEST_ENDPOINT", "https://display.example.invalid")
	raw := []byte(`
version: 2
timezone: UTC
database: {driver: memory}
storage: {driver: filesystem, directory: ./objects}
display:
  adapter: fake
  base_url: ${TEST_ENDPOINT}
  max_fps: 1
  blackout: {start: "21:00", end: "09:00"}
operator:
  test_window_until: "2026-08-28T23:30:00+10:00"
scheduler:
  default_lease: 24h
  selection: weighted_random
  default_cooldown: 24h
inference:
  allowed_window: {start: "09:00", end: "21:00"}
  blackout_behavior: suspend
  model_profile: test
  default_thinking: low
  thinking_change_policy: operator_only
  lease_budget:
    max_input_tokens: 1000
    max_output_tokens: 200
    max_model_calls: 4
    max_active_runtime: 10m
    max_model_scene_commits: 4
  per_call: {max_output_tokens: 100}
model_profiles:
  test:
    provider: fixture
    model: fixture-1
    thinking: {default: low, allowed: [low]}
    billing: {mode: unknown}
personas:
  - id: fixture
    display_name: Fixture
    enabled: true
    weight: 1
runtime: {driver: disabled, workspace_root: ./workspaces}
`)

	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Display.BaseURL != "https://display.example.invalid" {
		t.Fatalf("base URL = %q", cfg.Display.BaseURL)
	}
	if got := cfg.Scheduler.DefaultLease.Duration(); got != 24*time.Hour {
		t.Fatalf("default lease = %s", got)
	}
	if cfg.Operator.TestWindowUntil != "2026-08-28T23:30:00+10:00" {
		t.Fatalf("test window deadline = %q", cfg.Operator.TestWindowUntil)
	}
}

func TestParseRejectsInvalidTestWindowDeadline(t *testing.T) {
	raw := []byte(`
version: 2
timezone: UTC
display: {adapter: fake, max_fps: 1, blackout: {start: "21:00", end: "09:00"}}
operator: {test_window_until: tomorrow}
scheduler: {default_lease: 24h, selection: weighted_random, default_cooldown: 24h}
inference:
  allowed_window: {start: "09:00", end: "21:00"}
  thinking_change_policy: operator_only
  lease_budget: {max_input_tokens: 1, max_output_tokens: 1, max_model_calls: 1, max_active_runtime: 1s, max_model_scene_commits: 1}
  per_call: {max_output_tokens: 1}
`)
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "must be an RFC3339 timestamp") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	raw := []byte("version: 2\ntimezone: UTC\nunexpected: true\n")
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsModelControlledThinking(t *testing.T) {
	raw := []byte(`
version: 2
timezone: UTC
display: {adapter: fake, max_fps: 1, blackout: {start: "21:00", end: "09:00"}}
scheduler: {default_lease: 24h, selection: weighted_random, default_cooldown: 24h}
inference:
  allowed_window: {start: "09:00", end: "21:00"}
  thinking_change_policy: persona
  lease_budget: {max_input_tokens: 1, max_output_tokens: 1, max_model_calls: 1, max_active_runtime: 1s, max_model_scene_commits: 1}
  per_call: {max_output_tokens: 1}
`)
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "must be operator_only") {
		t.Fatalf("Parse() error = %v", err)
	}
}
