package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcm/pixel-steward/internal/config"
)

func TestWriteHermesConfigRestrictsExecutionToStudio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	profile := config.ModelProfile{Provider: "example", Model: "example-model", Endpoint: "https://example.invalid/v1"}
	if err := writeHermesConfig(path, "/usr/local/bin/pixel-steward", "http://controller:8080", "lease-token",
		"custom", profile, "credential", "high", 12, 4096, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	toolsets := value["platform_toolsets"].(map[string]any)["cli"].([]any)
	if len(toolsets) != 1 || toolsets[0] != "studio" {
		t.Fatalf("toolsets = %#v", toolsets)
	}
	mcp := value["mcp_servers"].(map[string]any)
	if len(mcp) != 1 || mcp["studio"] == nil {
		t.Fatalf("mcp servers = %#v", mcp)
	}
	agent := value["agent"].(map[string]any)
	if agent["reasoning_effort"] != "high" || agent["max_turns"] != float64(12) {
		t.Fatalf("agent = %#v", agent)
	}
	auxiliary := value["auxiliary"].(map[string]any)["title_generation"].(map[string]any)
	if auxiliary["enabled"] != false {
		t.Fatalf("automatic title inference was not disabled: %#v", auxiliary)
	}
	if mode := rawMode(t, path); mode.Perm() != 0o600 {
		t.Fatalf("config permissions = %v, want 0600", mode.Perm())
	}
}

func rawMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
