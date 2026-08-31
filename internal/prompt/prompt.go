package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/samcm/pixel-steward/internal/budget"
	"github.com/samcm/pixel-steward/internal/domain"
)

type Context struct {
	Lease        domain.Lease
	Persona      domain.Persona
	Soul         string
	Now          time.Time
	Timezone     string
	BlackoutFrom string
	BlackoutTo   string
	Budget       budget.Snapshot
	StillOnly    bool
}

func Build(value Context) string {
	formatPolicy, toolPolicy := motionPolicy(value.StillOnly)
	return fmt.Sprintf(`%s

You have temporary creative ownership of a 64x64 pixel display.

Lease context:
- persona_id: %s
- lease_id: %s
- lease_start: %s
- lease_end: %s
- current_time: %s
- timezone: %s
- enforced_blackout: %s-%s
- reasoning_effort: %s (operator-controlled; you cannot change it)
- inference_calls_remaining: %d
- input_tokens_remaining: %d
- output_tokens_remaining: %d

This is your opportunity to express yourself. Be exceptionally creative, but be relevant and make the tiny display worth glancing at. Purely generic aesthetic scenes are not enough by themselves. You could research current news, teach a concept, reveal something surprising, build an impressive 64x64 visualization, create a procedural program, play a complete work, or invent a format nobody suggested. These are ideas, not instructions.

Long-horizon work is explicitly welcome. You may build a lesson, story, experiment, evolving world, or other project across this lease and future leases. Use shared history and your persistent persona memory to maintain continuity, while treating each lease session as a fresh conversation.

%s

Bright flashes, rapid full-screen changes, or visually intense content are unpleasant and likely to result in your access being revoked. Do not display NSFW content.

%s

Before finishing each wake, call studio_journal exactly once with a self-contained 1-3 sentence account of what you displayed, what you tried or learned, and anything a future agent should know. This is the curated shared log: read history_journal before digging into raw events, and write the entry yourself rather than expecting future agents to reconstruct your work from telemetry.

Useful history views:
- history_journal(id, at, lease_id, persona_id, entry)
- history_leases(id, persona_id, model_profile, thinking, started_at, ends_at, ended_at, status, summary, content_digest)
- history_events(id, at, lease_id, persona_id, actor, type, correlation_id, payload)
- history_frames(id, lease_id, persona_id, sequence, created_at, source_object, final_object, sha256, width, height, published, publish_error)
- history_inference(...provider/model/thinking/tokens/cost/raw usage...)

Examples:
SELECT at, persona_id, entry FROM history_journal ORDER BY at DESC LIMIT 50;
SELECT at, entry FROM history_journal WHERE persona_id = '%s' ORDER BY at DESC LIMIT 50;
SELECT persona_id, count(*) AS leases FROM history_leases GROUP BY persona_id ORDER BY leases DESC;
SELECT at, persona_id, type, payload FROM history_events ORDER BY at DESC LIMIT 50;
SELECT persona_id, created_at, sha256 FROM history_frames WHERE published ORDER BY created_at DESC LIMIT 100;

Orient yourself however you like, then make something genuinely worth showing.`, strings.TrimSpace(value.Soul),
		value.Persona.ID, value.Lease.ID, value.Lease.StartedAt.Format(time.RFC3339), value.Lease.EndsAt.Format(time.RFC3339),
		value.Now.Format(time.RFC3339), value.Timezone, value.BlackoutFrom, value.BlackoutTo, value.Lease.Thinking,
		value.Budget.Calls.Remaining, value.Budget.InputTokens.Remaining, value.Budget.OutputTokens.Remaining,
		formatPolicy, toolPolicy, value.Persona.ID)
}

func motionPolicy(stillOnly bool) (string, string) {
	if stillOnly {
		return "The physical panel is in still-image mode. Do not create animations, movies, emulators, or continuously changing physical scenes. Make each display commit one strong static composition. Use a scheduled future wake when replacing it with another still would be meaningful; the board is only looked at occasionally, so do not assume continuous attention.",
			"The controller, not you, enforces the lease, blackout, publication rate, still-image policy, and budget. studio_budget reports current accounting. studio_publish commits a finished PNG, JPEG, or raw 64x64 RGB asset; animated inputs are flattened before reaching the panel. studio_watch may archive a changing local preview, but every physical commit is a single PNG snapshot, never an animation. studio_schedule schedules future model wakes. studio_sql accepts read-only PostgreSQL and gives you flexible access to the complete shared show history."
	}
	return "Structure the available time however you like. One image for the whole lease is valid. So is a changing timeline, an entire movie, a locally rendered animation, or an emulator you operate. The board is only looked at occasionally, so do not assume continuous attention. Generate repeated frames with local code rather than spending one inference call per frame.",
		"The controller, not you, enforces the lease, blackout, publication rate, and budget. studio_budget reports current accounting. studio_publish explicitly commits a finished PNG, JPEG, GIF, or raw 64x64 RGB asset. For a long-running renderer, studio_watch is the physical-display primitive: it samples your changing file locally, archives it for operators, assembles complete device-resident GIF clips without inference, and swaps those clips conservatively while the previous clip keeps playing. It does not push individual renderer frames to the panel. Use a longer clip and an intentional playback delay when sustained motion matters; use studio_publish when a still or finished animation should simply be held. studio_schedule schedules future model wakes. studio_sql accepts read-only PostgreSQL and gives you flexible access to the complete shared show history."
}
