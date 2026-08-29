package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcm/pixel-steward/internal/agent"
	"github.com/samcm/pixel-steward/internal/budget"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/domain"
	"github.com/samcm/pixel-steward/internal/store"
)

// Hermes runs one fresh Hermes session per wake while retaining a private,
// durable Hermes home for each persona. The only enabled toolset is the
// controller's lease-scoped Studio MCP server; code and Docker operations
// therefore execute in the configured sandbox, never in this process.
type Hermes struct {
	config     config.Runtime
	store      store.Store
	executable string
	clock      func() time.Time
}

type hermesUsage struct {
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	CostStatus       string  `json:"cost_status"`
	CostSource       string  `json:"cost_source"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	APICalls         int64   `json:"api_calls"`
	Model            string  `json:"model"`
	Provider         string  `json:"provider"`
	SessionID        string  `json:"session_id"`
	Completed        bool    `json:"completed"`
	Failed           bool    `json:"failed"`
	Failure          string  `json:"failure"`
	ServiceTier      string  `json:"service_tier"`
}

func NewHermes(value config.Runtime, database store.Store, executable string) (*Hermes, error) {
	if len(value.Command) == 0 {
		return nil, errors.New("hermes command is required")
	}
	if executable == "" {
		return nil, errors.New("pixel-steward executable path is required")
	}
	return &Hermes{config: value, store: database, executable: executable, clock: time.Now}, nil
}

func (h *Hermes) Run(parent context.Context, wake agent.Wake) error {
	now := h.clock()
	snapshot := wake.Budget.Snapshot(now)
	turns := min(h.config.MaxSteps, int(snapshot.Calls.Remaining))
	promptEstimate := max(int64(1), int64(len(wake.Prompt))/4)
	turns = min(turns, int(snapshot.InputTokens.Remaining/promptEstimate))
	if turns <= 0 || snapshot.OutputTokens.Remaining <= 0 {
		return budget.ErrExhausted
	}
	perCallOutput := min(snapshot.PerCallOutputLimit, snapshot.OutputTokens.Remaining/int64(turns))
	if perCallOutput <= 0 {
		return budget.ErrExhausted
	}

	timeout := h.config.MaxWakeTime.Duration()
	if remaining := time.Until(wake.Lease.EndsAt); remaining < timeout {
		timeout = remaining
	}
	if timeout <= 0 {
		return context.DeadlineExceeded
	}

	workspace := filepath.Join(h.config.WorkspaceRoot, wake.Lease.ID)
	identity := sha256.Sum256([]byte(wake.Persona.ID))
	hermesHome := filepath.Join(h.config.WorkspaceRoot, ".hermes", hex.EncodeToString(identity[:]))
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(hermesHome, 0o700); err != nil {
		return err
	}

	credential := ""
	if wake.Profile.CredentialEnv != "" {
		credential = os.Getenv(wake.Profile.CredentialEnv)
		if credential == "" {
			return fmt.Errorf("credential environment %s is empty", wake.Profile.CredentialEnv)
		}
	}
	provider := wake.Profile.Provider
	if wake.Profile.Endpoint != "" {
		provider = "custom"
	}
	if err := writeHermesConfig(filepath.Join(hermesHome, "config.yaml"), h.executable, h.config.ControllerURL,
		wake.AgentToken, provider, wake.Profile, credential, wake.Lease.Thinking, turns, perCallOutput, timeout); err != nil {
		return err
	}

	promptPath := filepath.Join(workspace, "hermes-prompt.txt")
	usagePath := filepath.Join(workspace, "hermes-usage.json")
	eventsPath := filepath.Join(workspace, "hermes-events.jsonl")
	if err := os.WriteFile(promptPath, []byte(wake.Prompt), 0o600); err != nil {
		return err
	}
	_ = os.Remove(usagePath)
	_ = os.Remove(eventsPath)

	reservation, err := wake.Budget.Reserve(now, budget.Estimate{
		InputTokens: promptEstimate, MaxOutputTokens: perCallOutput,
	})
	if err != nil {
		return err
	}
	reservationActive := true
	defer func() {
		if reservationActive {
			_ = wake.Budget.Cancel(reservation.ID)
		}
	}()

	h.appendEvent(wake, "runtime.step_start", jsonValue(map[string]any{
		"type": "step_start", "part": map[string]any{"runtime": "hermes", "max_turns": turns},
	}))
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	commandArgs := append([]string{}, h.config.Command[1:]...)
	commandArgs = append(commandArgs,
		"--prompt-file", promptPath,
		"--usage-file", usagePath,
		"--events-file", eventsPath,
		"--model", wake.Profile.Model,
		"--provider", provider,
		"--reasoning", wake.Lease.Thinking,
		"--toolsets", "studio",
	)
	command := exec.CommandContext(ctx, h.config.Command[0], commandArgs...)
	command.Dir = workspace
	command.Env = append(withoutEnvironment(filteredEnvironment(os.Environ()), "HERMES_HOME", "HOME"),
		"HERMES_HOME="+hermesHome, "HOME="+hermesHome)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	ended := h.clock()

	usageRaw, readUsageErr := os.ReadFile(usagePath)
	var usage hermesUsage
	if readUsageErr == nil {
		readUsageErr = json.Unmarshal(usageRaw, &usage)
	}
	if events, eventErr := os.Open(eventsPath); eventErr == nil {
		scanner := bufio.NewScanner(events)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			raw := append([]byte(nil), scanner.Bytes()...)
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &event) == nil && event.Type != "" {
				h.appendEvent(wake, "runtime."+event.Type, raw)
			}
		}
		_ = events.Close()
	} else if text := strings.TrimSpace(stdout.String()); text != "" {
		h.appendEvent(wake, "runtime.text", jsonValue(map[string]any{
			"type": "text", "part": map[string]string{"text": text},
		}))
	}

	modelCalls := usage.APICalls
	if modelCalls <= 0 {
		modelCalls = 1
	}
	costMicros := int64(usage.EstimatedCostUSD * 1_000_000)
	status := "completed"
	stopReason := "completed"
	if runErr != nil || readUsageErr != nil || usage.Failed {
		status = "failed"
		stopReason = strings.TrimSpace(usage.Failure)
		if stopReason == "" {
			stopReason = strings.TrimSpace(stderr.String())
		}
		if stopReason == "" && runErr != nil {
			stopReason = runErr.Error()
		}
		if stopReason == "" && readUsageErr != nil {
			stopReason = readUsageErr.Error()
		}
	}
	request := domain.InferenceRequest{
		ID: newRequestID(wake.Lease.ID, 0), LeaseID: wake.Lease.ID, PersonaID: wake.Persona.ID,
		Provider: wake.Profile.Provider, Model: wake.Profile.Model, Thinking: wake.Lease.Thinking,
		ThinkingSource: "controller_config", ProviderRequestID: usage.SessionID, StartedAt: reservation.StartedAt,
		EndedAt: &ended, Status: status, StopReason: stopReason, ModelCalls: modelCalls,
		PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, ReasoningTokens: usage.ReasoningTokens,
		CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
		EstimatedMeteredMicros: costMicros, RawUsage: usageRaw,
	}
	_ = h.store.UpsertInferenceRequest(context.Background(), request)
	if readUsageErr == nil {
		_ = wake.Budget.Complete(reservation.ID, budget.Actual{
			Tokens: budget.TokenUsage{Input: usage.InputTokens, Output: usage.OutputTokens, Reasoning: usage.ReasoningTokens,
				CacheRead: usage.CacheReadTokens, CacheWrite: usage.CacheWriteTokens},
			Cost: budget.CostUsage{EstimatedMeteredMicros: costMicros}, ActiveRuntime: ended.Sub(reservation.StartedAt),
			ModelCalls: modelCalls,
		}, ended)
		reservationActive = false
	}
	h.appendEvent(wake, "runtime.step_finish", jsonValue(map[string]any{
		"type": "step_finish", "part": map[string]any{
			"messageID": usage.SessionID, "reason": stopReason, "cost": usage.EstimatedCostUSD,
			"tokens": map[string]any{"input": usage.InputTokens, "output": usage.OutputTokens,
				"reasoning": usage.ReasoningTokens, "cache": map[string]int64{"read": usage.CacheReadTokens, "write": usage.CacheWriteTokens}},
			"api_calls": usage.APICalls, "runtime": "hermes",
		},
	}))

	if readUsageErr != nil {
		return fmt.Errorf("hermes usage report: %w", readUsageErr)
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("hermes exited: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	if usage.Failed {
		return fmt.Errorf("hermes failed: %s", stopReason)
	}
	return nil
}

func writeHermesConfig(path, executable, controllerURL, token, provider string, profile config.ModelProfile,
	credential, thinking string, turns int, perCallOutput int64, timeout time.Duration) error {
	model := map[string]any{
		"provider": provider, "default": profile.Model, "max_tokens": perCallOutput,
	}
	if profile.Endpoint != "" {
		model["base_url"] = profile.Endpoint
	}
	if credential != "" {
		model["api_key"] = credential
	}
	value := map[string]any{
		"model": model,
		"agent": map[string]any{
			"reasoning_effort": thinking, "max_turns": turns,
			"run_budget_seconds": max(1, int(timeout.Seconds())),
		},
		"memory":            map[string]bool{"memory_enabled": true},
		"auxiliary":         map[string]any{"title_generation": map[string]bool{"enabled": false}},
		"approvals":         map[string]string{"mode": "off"},
		"hooks_auto_accept": true,
		"platform_toolsets": map[string][]string{"cli": {"studio"}},
		"mcp_servers": map[string]any{"studio": map[string]any{
			"command": executable, "args": []string{"mcp", "--api", controllerURL, "--token", token}, "enabled": true,
		}},
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
}

func withoutEnvironment(values []string, names ...string) []string {
	prefixes := make([]string, len(names))
	for index, name := range names {
		prefixes[index] = name + "="
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		keep := true
		for _, prefix := range prefixes {
			if strings.HasPrefix(value, prefix) {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, value)
		}
	}
	return result
}

func (h *Hermes) appendEvent(wake agent.Wake, kind string, payload json.RawMessage) {
	_, _ = h.store.AppendEvent(context.Background(), domain.Event{At: h.clock(), LeaseID: wake.Lease.ID,
		PersonaID: wake.Persona.ID, Actor: "runtime", Type: kind, Payload: payload})
}

func jsonValue(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
