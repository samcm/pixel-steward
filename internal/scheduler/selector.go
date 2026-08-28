package scheduler

import (
	"errors"
	"math/rand"
	"slices"
	"time"

	"github.com/samcm/pixel-steward/internal/domain"
)

var ErrNoEligiblePersona = errors.New("no eligible persona")

type Candidate struct {
	ID       string `json:"id"`
	Weight   int    `json:"weight"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

type Decision struct {
	At         time.Time   `json:"at"`
	Seed       int64       `json:"seed"`
	PreviousID string      `json:"previous_id,omitempty"`
	Candidates []Candidate `json:"candidates"`
	SelectedID string      `json:"selected_id"`
}

type Selector struct {
	AvoidImmediateRepeat bool
}

func (s Selector) Select(at time.Time, seed int64, personas []domain.Persona, leases []domain.Lease) (Decision, error) {
	decision := Decision{At: at, Seed: seed}
	lastEnded := make(map[string]time.Time)
	for _, lease := range leases {
		ended := lease.EndsAt
		if lease.EndedAt != nil {
			ended = *lease.EndedAt
		}
		if ended.After(lastEnded[lease.PersonaID]) {
			lastEnded[lease.PersonaID] = ended
		}
		if decision.PreviousID == "" {
			decision.PreviousID = lease.PersonaID
		}
	}

	slices.SortFunc(personas, func(a, b domain.Persona) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})

	eligible := make([]domain.Persona, 0, len(personas))
	for _, persona := range personas {
		candidate := Candidate{ID: persona.ID, Weight: persona.Weight}
		switch {
		case !persona.Enabled:
			candidate.Reason = "disabled"
		case persona.Weight <= 0:
			candidate.Reason = "zero_weight"
		case !lastEnded[persona.ID].IsZero() && at.Before(lastEnded[persona.ID].Add(persona.Cooldown)):
			candidate.Reason = "cooldown"
		default:
			candidate.Eligible = true
			eligible = append(eligible, persona)
		}
		decision.Candidates = append(decision.Candidates, candidate)
	}

	if s.AvoidImmediateRepeat && len(eligible) > 1 && decision.PreviousID != "" {
		filtered := eligible[:0]
		for _, persona := range eligible {
			if persona.ID != decision.PreviousID {
				filtered = append(filtered, persona)
				continue
			}
			for index := range decision.Candidates {
				if decision.Candidates[index].ID == persona.ID {
					decision.Candidates[index].Eligible = false
					decision.Candidates[index].Reason = "immediate_repeat"
				}
			}
		}
		eligible = filtered
	}

	var total int
	for _, persona := range eligible {
		total += persona.Weight
	}
	if total == 0 {
		return decision, ErrNoEligiblePersona
	}

	pick := rand.New(rand.NewSource(seed)).Intn(total)
	for _, persona := range eligible {
		pick -= persona.Weight
		if pick < 0 {
			decision.SelectedID = persona.ID
			return decision, nil
		}
	}

	return decision, ErrNoEligiblePersona
}
