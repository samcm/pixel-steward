package prompt

import (
	"strings"
	"testing"

	"github.com/samcm/pixel-steward/internal/domain"
)

func TestBuildIncludesCreativeAndSafetyContract(t *testing.T) {
	text := Build(Context{Persona: domain.Persona{ID: "example"}, Lease: domain.Lease{ID: "lease", Thinking: "low"}})
	for _, expected := range []string{"Long-horizon work is explicitly welcome", "studio_sql", "Bright flashes", "Do not display NSFW", "operator-controlled"} {
		if !strings.Contains(text, expected) {
			t.Errorf("prompt does not contain %q", expected)
		}
	}
}
