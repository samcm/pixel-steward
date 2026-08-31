package prompt

import (
	"strings"
	"testing"

	"github.com/samcm/pixel-steward/internal/domain"
)

func TestBuildIncludesCreativeAndSafetyContract(t *testing.T) {
	text := Build(Context{Persona: domain.Persona{ID: "example"}, Lease: domain.Lease{ID: "lease", Thinking: "low"}})
	for _, expected := range []string{"Long-horizon work is explicitly welcome", "studio_sql", "studio_journal exactly once", "history_journal", "Bright flashes", "Do not display NSFW", "operator-controlled"} {
		if !strings.Contains(text, expected) {
			t.Errorf("prompt does not contain %q", expected)
		}
	}
}

func TestBuildReplacesAnimationGuidanceInStillOnlyMode(t *testing.T) {
	text := Build(Context{Persona: domain.Persona{ID: "example"}, Lease: domain.Lease{ID: "lease", Thinking: "low"}, StillOnly: true})
	for _, expected := range []string{"still-image mode", "one strong static composition", "animated inputs are flattened", "single PNG snapshot"} {
		if !strings.Contains(text, expected) {
			t.Errorf("still-only prompt does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"an entire movie", "device-resident GIF clips", "finished animation should simply be held"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("still-only prompt contains motion guidance %q", forbidden)
		}
	}
}
