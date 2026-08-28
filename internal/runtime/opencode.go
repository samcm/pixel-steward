package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samcm/pixel-steward/internal/agent"
	"github.com/samcm/pixel-steward/internal/budget"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/domain"
	"github.com/samcm/pixel-steward/internal/store"
)

type OpenCode struct {
	config     config.Runtime
	store      store.Store
	executable string
	clock      func() time.Time
}

func NewOpenCode(value config.Runtime, database store.Store, executable string) (*OpenCode, error) {
	if len(value.Command) == 0 {
		return nil, errors.New("opencode command is required")
	}
	if executable == "" {
		return nil, errors.New("pixel-steward executable path is required")
	}
	return &OpenCode{config: value, store: database, executable: executable, clock: time.Now}, nil
}

func (o *OpenCode) Run(parent context.Context, wake agent.Wake) error {
	now := o.clock()
	snapshot := wake.Budget.Snapshot(now)
	steps := min(o.config.MaxSteps, int(snapshot.Calls.Remaining))
	perCallOutput := snapshot.OutputTokens.Remaining
	if steps > 0 {
		perCallOutput /= int64(steps)
	}
	if perCallOutput <= 0 || steps <= 0 {
		return budget.ErrExhausted
	}
	promptEstimate := max(int64(1), int64(len(wake.Prompt))/4)
	if promptEstimate*int64(steps) > snapshot.InputTokens.Remaining {
		steps = int(snapshot.InputTokens.Remaining / promptEstimate)
	}
	if steps <= 0 {
		return budget.ErrExhausted
	}
	perCallOutput = min(perCallOutput, snapshot.OutputTokens.Remaining/int64(steps), snapshot.PerCallOutputLimit)

	workspace := filepath.Join(o.config.WorkspaceRoot, wake.Lease.ID)
	supervisor := filepath.Join(workspace, ".pixel-steward")
	configHome := filepath.Join(supervisor, "config")
	dataHome := filepath.Join(supervisor, "data")
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(dataHome, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(configHome, "opencode.json")
	credential := ""
	if wake.Profile.CredentialEnv != "" {
		credential = os.Getenv(wake.Profile.CredentialEnv)
		if credential == "" {
			return fmt.Errorf("credential environment %s is empty", wake.Profile.CredentialEnv)
		}
		authDirectory := filepath.Join(dataHome, "opencode")
		if err := os.MkdirAll(authDirectory, 0o700); err != nil {
			return err
		}
		auth, _ := json.Marshal(map[string]any{wake.Profile.Provider: map[string]string{"type": "api", "key": credential}})
		if err := os.WriteFile(filepath.Join(authDirectory, "auth.json"), auth, 0o600); err != nil {
			return err
		}
	}
	if err := writeOpenCodeConfig(configPath, o.executable, o.config.ControllerURL, wake.AgentToken, steps, wake.Profile, credential); err != nil {
		return err
	}

	timeout := o.config.MaxWakeTime.Duration()
	if remaining := time.Until(wake.Lease.EndsAt); remaining < timeout {
		timeout = remaining
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, o.config.Command[0], append(o.config.Command[1:],
		"run", "--pure", "--auto", "--format", "json", "--agent", "steward", "--model",
		wake.Profile.Provider+"/"+wake.Profile.Model, "--variant", wake.Lease.Thinking, "--dir", workspace, wake.Prompt)...)
	command.Env = append(filteredEnvironment(os.Environ()),
		"OPENCODE_CONFIG="+configPath, "XDG_CONFIG_HOME="+configHome, "XDG_DATA_HOME="+dataHome)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	stderrDone := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stderr, 1<<20))
		stderrDone <- data
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	stepIndex := 0
	var activeReservation *budget.Reservation
	var reservationErr error
	defer func() {
		if activeReservation != nil {
			_ = wake.Budget.Cancel(activeReservation.ID)
		}
	}()
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		var event openCodeEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			o.appendEvent(wake, "runtime.output", json.RawMessage(strconv.Quote(string(raw))))
			continue
		}
		o.appendEvent(wake, "runtime."+event.Type, raw)
		if event.Type == "step_start" {
			if activeReservation != nil {
				continue
			}
			reservation, reserveErr := wake.Budget.Reserve(o.clock(), budget.Estimate{InputTokens: promptEstimate, MaxOutputTokens: perCallOutput})
			if reserveErr != nil {
				reservationErr = reserveErr
				cancel()
				break
			}
			activeReservation = &reservation
			continue
		}
		if event.Type != "step_finish" || activeReservation == nil {
			continue
		}
		ended := o.clock()
		usage := event.Part.Tokens
		costMicros := int64(event.Part.Cost * 1_000_000)
		request := domain.InferenceRequest{
			ID: newRequestID(wake.Lease.ID, stepIndex), LeaseID: wake.Lease.ID, PersonaID: wake.Persona.ID,
			Provider: wake.Profile.Provider, Model: wake.Profile.Model, Thinking: wake.Lease.Thinking,
			ThinkingSource: "controller_config", ProviderRequestID: event.Part.MessageID, StartedAt: activeReservation.StartedAt,
			EndedAt: &ended, Status: "completed", StopReason: event.Part.Reason, PromptTokens: usage.Input,
			CompletionTokens: usage.Output, ReasoningTokens: usage.Reasoning, CacheReadTokens: usage.Cache.Read,
			CacheWriteTokens: usage.Cache.Write, EstimatedMeteredMicros: costMicros, ProviderReportedMicros: &costMicros,
			RawUsage: raw,
		}
		_ = o.store.UpsertInferenceRequest(ctx, request)
		_ = wake.Budget.Complete(activeReservation.ID, budget.Actual{
			Tokens: budget.TokenUsage{Input: usage.Input, Output: usage.Output, Reasoning: usage.Reasoning,
				CacheRead: usage.Cache.Read, CacheWrite: usage.Cache.Write},
			Cost:          budget.CostUsage{EstimatedMeteredMicros: costMicros, ProviderReportedMicros: &costMicros},
			ActiveRuntime: ended.Sub(activeReservation.StartedAt),
		}, ended)
		activeReservation = nil
		stepIndex++
		if wake.Budget.Snapshot(ended).Status == "exhausted" {
			cancel()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	stderrOutput := <-stderrDone
	if reservationErr != nil {
		return reservationErr
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("opencode exited: %w: %s", waitErr, strings.TrimSpace(string(stderrOutput)))
	}
	return nil
}

type openCodeEvent struct {
	Type string `json:"type"`
	Part struct {
		MessageID string  `json:"messageID"`
		Reason    string  `json:"reason"`
		Cost      float64 `json:"cost"`
		Tokens    struct {
			Input     int64 `json:"input"`
			Output    int64 `json:"output"`
			Reasoning int64 `json:"reasoning"`
			Cache     struct {
				Read  int64 `json:"read"`
				Write int64 `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	} `json:"part"`
}

func writeOpenCodeConfig(path, executable, controllerURL, token string, steps int, profile config.ModelProfile, credential string) error {
	value := map[string]any{
		"$schema":    "https://opencode.ai/config.json",
		"autoupdate": false,
		"permission": map[string]string{"*": "deny", "studio_*": "allow"},
		"agent": map[string]any{"steward": map[string]any{
			"description": "Pixel Steward lease persona", "mode": "primary", "steps": steps,
			"permission": map[string]string{"*": "deny", "studio_*": "allow"},
		}},
		"mcp": map[string]any{"studio": map[string]any{
			"type": "local", "enabled": true,
			"command": []string{executable, "mcp", "--api", controllerURL, "--token", token},
		}},
	}
	if profile.Endpoint != "" {
		value["provider"] = map[string]any{profile.Provider: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": profile.Provider,
			"options": map[string]string{"baseURL": profile.Endpoint, "apiKey": credential},
			"models":  map[string]any{profile.Model: map[string]any{"name": profile.Model, "tools": true, "reasoning": true}},
		}}
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
}

func filteredEnvironment(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "OPENCODE_CONFIG=") || strings.HasPrefix(value, "XDG_CONFIG_HOME=") || strings.HasPrefix(value, "XDG_DATA_HOME=") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (o *OpenCode) appendEvent(wake agent.Wake, kind string, payload json.RawMessage) {
	_, _ = o.store.AppendEvent(context.Background(), domain.Event{At: o.clock(), LeaseID: wake.Lease.ID,
		PersonaID: wake.Persona.ID, Actor: "runtime", Type: kind, Payload: payload})
}

func newRequestID(leaseID string, index int) string {
	return fmt.Sprintf("%s_call_%06d_%d", leaseID, index+1, time.Now().UnixNano())
}
