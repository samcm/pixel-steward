package agent

import (
	"context"

	"github.com/samcm/pixel-steward/internal/budget"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/domain"
)

type Wake struct {
	Lease      domain.Lease
	Persona    domain.Persona
	Profile    config.ModelProfile
	Prompt     string
	AgentToken string
	Budget     *budget.Ledger
}

type Runner interface {
	Run(context.Context, Wake) error
}

type Disabled struct{}

func (Disabled) Run(context.Context, Wake) error { return nil }
