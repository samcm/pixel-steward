package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const CurrentVersion = 2

type Config struct {
	Version       int                     `yaml:"version"`
	Timezone      string                  `yaml:"timezone"`
	HTTP          HTTP                    `yaml:"http"`
	Database      Database                `yaml:"database"`
	Storage       Storage                 `yaml:"storage"`
	Display       Display                 `yaml:"display"`
	Operator      Operator                `yaml:"operator"`
	Scheduler     Scheduler               `yaml:"scheduler"`
	Inference     Inference               `yaml:"inference"`
	Runtime       Runtime                 `yaml:"runtime"`
	Sandbox       Sandbox                 `yaml:"sandbox"`
	ModelProfiles map[string]ModelProfile `yaml:"model_profiles"`
	Personas      []Persona               `yaml:"personas"`
}

// Operator contains temporary, explicitly configured control-plane overrides.
// TestWindowUntil is an absolute RFC3339 deadline so an override cannot be
// accidentally left enabled indefinitely.
type Operator struct {
	TestWindowUntil string `yaml:"test_window_until"`
}

type HTTP struct {
	Listen string   `yaml:"listen"`
	Auth   HTTPAuth `yaml:"auth"`
}

type HTTPAuth struct {
	Mode      string `yaml:"mode"`
	TokenFile string `yaml:"token_file"`
}

type Database struct {
	Driver string `yaml:"driver"`
	URL    string `yaml:"url"`
}

type Storage struct {
	Driver       string `yaml:"driver"`
	Directory    string `yaml:"directory"`
	Endpoint     string `yaml:"endpoint"`
	Region       string `yaml:"region"`
	Bucket       string `yaml:"bucket"`
	AccessKeyEnv string `yaml:"access_key_env"`
	SecretKeyEnv string `yaml:"secret_key_env"`
	UseTLS       bool   `yaml:"use_tls"`
}

type Display struct {
	Adapter  string   `yaml:"adapter"`
	BaseURL  string   `yaml:"base_url"`
	MaxFPS   float64  `yaml:"max_fps"`
	Blackout TimeSpan `yaml:"blackout"`
}

type TimeSpan struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type Scheduler struct {
	DefaultLease         Duration `yaml:"default_lease"`
	Selection            string   `yaml:"selection"`
	AvoidImmediateRepeat bool     `yaml:"avoid_immediate_repeat"`
	DefaultCooldown      Duration `yaml:"default_cooldown"`
}

type Inference struct {
	AllowedWindow        TimeSpan  `yaml:"allowed_window"`
	BlackoutBehavior     string    `yaml:"blackout_behavior"`
	ModelProfile         string    `yaml:"model_profile"`
	DefaultThinking      string    `yaml:"default_thinking"`
	ThinkingChangePolicy string    `yaml:"thinking_change_policy"`
	LeaseBudget          Budget    `yaml:"lease_budget"`
	PerCall              CallLimit `yaml:"per_call"`
}

type Budget struct {
	MaxInputTokens       int64    `yaml:"max_input_tokens"`
	MaxOutputTokens      int64    `yaml:"max_output_tokens"`
	MaxModelCalls        int64    `yaml:"max_model_calls"`
	MaxActiveRuntime     Duration `yaml:"max_active_runtime"`
	MaxCostUSD           *float64 `yaml:"max_cost_usd"`
	MaxModelSceneCommits int64    `yaml:"max_model_scene_commits"`
}

type CallLimit struct {
	MaxOutputTokens int64 `yaml:"max_output_tokens"`
}

type Runtime struct {
	Driver        string   `yaml:"driver"`
	WorkspaceRoot string   `yaml:"workspace_root"`
	Command       []string `yaml:"command"`
	ControllerURL string   `yaml:"controller_url"`
	MaxSteps      int      `yaml:"max_steps"`
	MaxWakeTime   Duration `yaml:"max_wake_time"`
}

type Sandbox struct {
	Driver         string   `yaml:"driver"`
	BaseURL        string   `yaml:"base_url"`
	TokenEnv       string   `yaml:"token_env"`
	LocalRoot      string   `yaml:"local_root"`
	MaxExecTime    Duration `yaml:"max_exec_time"`
	ExecCommand    []string `yaml:"exec_command"`
	ReadCommand    []string `yaml:"read_command"`
	SuspendCommand []string `yaml:"suspend_command"`
	ResumeCommand  []string `yaml:"resume_command"`
	ResetCommand   []string `yaml:"reset_command"`
}

type ModelProfile struct {
	Provider      string   `yaml:"provider"`
	Model         string   `yaml:"model"`
	Endpoint      string   `yaml:"endpoint"`
	CredentialEnv string   `yaml:"credential_env"`
	Thinking      Thinking `yaml:"thinking"`
	Billing       Billing  `yaml:"billing"`
}

type Thinking struct {
	Default      string   `yaml:"default"`
	Allowed      []string `yaml:"allowed"`
	Capabilities string   `yaml:"capabilities"`
	CacheImpact  string   `yaml:"cache_impact"`
}

type Billing struct {
	Mode                       string `yaml:"mode"`
	RateCard                   string `yaml:"rate_card"`
	PrivateRateCard            string `yaml:"private_rate_card"`
	PreferProviderReportedCost bool   `yaml:"prefer_provider_reported_cost"`
}

type Persona struct {
	ID             string   `yaml:"id"`
	DisplayName    string   `yaml:"display_name"`
	Enabled        bool     `yaml:"enabled"`
	Weight         int      `yaml:"weight"`
	Cooldown       Duration `yaml:"cooldown"`
	Lease          Duration `yaml:"lease"`
	Soul           string   `yaml:"soul"`
	Toolsets       []string `yaml:"toolsets"`
	BudgetOverride *Budget  `yaml:"budget"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	return Parse(raw)
}

func Parse(raw []byte) (Config, error) {
	expanded := os.ExpandEnv(string(raw))
	decoder := yaml.NewDecoder(bytes.NewBufferString(expanded))
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}

	if err := cfg.setDefaults(); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) setDefaults() error {
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.HTTP.Listen == "" {
		c.HTTP.Listen = "127.0.0.1:8080"
	}
	if c.HTTP.Auth.Mode == "" {
		c.HTTP.Auth.Mode = "disabled"
	}
	if c.Database.Driver == "" {
		c.Database.Driver = "memory"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "filesystem"
	}
	if c.Storage.Directory == "" {
		c.Storage.Directory = "./data/objects"
	}
	if c.Display.Adapter == "" {
		c.Display.Adapter = "fake"
	}
	if c.Display.MaxFPS == 0 {
		c.Display.MaxFPS = 1
	}
	if c.Scheduler.DefaultLease == 0 {
		c.Scheduler.DefaultLease = Duration(24 * time.Hour)
	}
	if c.Scheduler.DefaultCooldown == 0 {
		c.Scheduler.DefaultCooldown = Duration(24 * time.Hour)
	}
	if c.Scheduler.Selection == "" {
		c.Scheduler.Selection = "weighted_random"
	}
	if c.Inference.BlackoutBehavior == "" {
		c.Inference.BlackoutBehavior = "suspend"
	}
	if c.Inference.DefaultThinking == "" {
		c.Inference.DefaultThinking = "low"
	}
	if c.Inference.ThinkingChangePolicy == "" {
		c.Inference.ThinkingChangePolicy = "operator_only"
	}
	if c.Runtime.Driver == "" {
		c.Runtime.Driver = "disabled"
	}
	if c.Runtime.WorkspaceRoot == "" {
		c.Runtime.WorkspaceRoot = "./data/workspaces"
	}
	if c.Runtime.MaxWakeTime == 0 {
		c.Runtime.MaxWakeTime = Duration(20 * time.Minute)
	}
	if c.Runtime.MaxSteps == 0 {
		c.Runtime.MaxSteps = 8
	}
	if c.Sandbox.Driver == "" {
		c.Sandbox.Driver = "disabled"
	}
	if c.Sandbox.LocalRoot == "" {
		c.Sandbox.LocalRoot = "./data/sandboxes"
	}
	if c.Sandbox.MaxExecTime == 0 {
		c.Sandbox.MaxExecTime = Duration(10 * time.Minute)
	}

	return nil
}

func (c Config) Validate() error {
	var problems []error
	if c.Version != CurrentVersion {
		problems = append(problems, fmt.Errorf("version must be %d", CurrentVersion))
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		problems = append(problems, fmt.Errorf("timezone: %w", err))
	}
	if c.Display.MaxFPS <= 0 || c.Display.MaxFPS > 60 {
		problems = append(problems, errors.New("display.max_fps must be greater than zero and no more than 60"))
	}
	if err := validateTimeSpan("display.blackout", c.Display.Blackout); err != nil {
		problems = append(problems, err)
	}
	if c.Operator.TestWindowUntil != "" {
		if _, err := time.Parse(time.RFC3339, c.Operator.TestWindowUntil); err != nil {
			problems = append(problems, fmt.Errorf("operator.test_window_until must be an RFC3339 timestamp: %w", err))
		}
	}
	if err := validateTimeSpan("inference.allowed_window", c.Inference.AllowedWindow); err != nil {
		problems = append(problems, err)
	}
	if c.Inference.ThinkingChangePolicy != "operator_only" {
		problems = append(problems, errors.New("inference.thinking_change_policy must be operator_only"))
	}
	if c.Inference.BlackoutBehavior != "suspend" && c.Inference.BlackoutBehavior != "terminate" {
		problems = append(problems, errors.New("inference.blackout_behavior must be suspend or terminate"))
	}
	profile, hasInferenceProfile := c.ModelProfiles[c.Inference.ModelProfile]
	if c.Runtime.Driver == "opencode" && !hasInferenceProfile {
		problems = append(problems, fmt.Errorf("inference.model_profile %q does not exist", c.Inference.ModelProfile))
	}
	if hasInferenceProfile && len(profile.Thinking.Allowed) > 0 && !contains(profile.Thinking.Allowed, c.Inference.DefaultThinking) {
		problems = append(problems, fmt.Errorf("inference.default_thinking %q is not supported by model profile %q", c.Inference.DefaultThinking, c.Inference.ModelProfile))
	}
	if err := validateBudget("inference.lease_budget", c.Inference.LeaseBudget); err != nil {
		problems = append(problems, err)
	}
	if c.Inference.PerCall.MaxOutputTokens <= 0 {
		problems = append(problems, errors.New("inference.per_call.max_output_tokens must be greater than zero"))
	}
	if c.Scheduler.DefaultLease.Duration() <= 0 {
		problems = append(problems, errors.New("scheduler.default_lease must be greater than zero"))
	}
	if c.Scheduler.Selection != "weighted_random" {
		problems = append(problems, errors.New("scheduler.selection must be weighted_random"))
	}
	if c.HTTP.Auth.Mode != "disabled" && c.HTTP.Auth.Mode != "bearer" {
		problems = append(problems, errors.New("http.auth.mode must be disabled or bearer"))
	}
	if c.HTTP.Auth.Mode == "bearer" && c.HTTP.Auth.TokenFile == "" {
		problems = append(problems, errors.New("http.auth.token_file is required for bearer auth"))
	}
	if c.Database.Driver != "memory" && c.Database.Driver != "postgres" {
		problems = append(problems, errors.New("database.driver must be memory or postgres"))
	}
	if c.Database.Driver == "postgres" && c.Database.URL == "" {
		problems = append(problems, errors.New("database.url is required for postgres"))
	}
	if c.Storage.Driver != "filesystem" && c.Storage.Driver != "s3" {
		problems = append(problems, errors.New("storage.driver must be filesystem or s3"))
	}
	if c.Storage.Driver == "s3" && (c.Storage.Endpoint == "" || c.Storage.Bucket == "" || c.Storage.AccessKeyEnv == "" || c.Storage.SecretKeyEnv == "") {
		problems = append(problems, errors.New("storage.endpoint, storage.bucket, storage.access_key_env, and storage.secret_key_env are required for s3"))
	}
	if c.Runtime.Driver != "disabled" && c.Runtime.Driver != "opencode" {
		problems = append(problems, errors.New("runtime.driver must be disabled or opencode"))
	}
	if c.Runtime.Driver == "opencode" && (len(c.Runtime.Command) == 0 || c.Runtime.ControllerURL == "") {
		problems = append(problems, errors.New("runtime.command and runtime.controller_url are required for opencode"))
	}
	if c.Runtime.MaxSteps <= 0 {
		problems = append(problems, errors.New("runtime.max_steps must be greater than zero"))
	}
	if c.Sandbox.Driver != "disabled" && c.Sandbox.Driver != "local" && c.Sandbox.Driver != "http" && c.Sandbox.Driver != "command" {
		problems = append(problems, errors.New("sandbox.driver must be disabled, local, http, or command"))
	}
	if c.Sandbox.Driver == "http" && (c.Sandbox.BaseURL == "" || c.Sandbox.TokenEnv == "") {
		problems = append(problems, errors.New("sandbox.base_url and sandbox.token_env are required for http sandbox"))
	}
	if c.Sandbox.Driver == "command" && (len(c.Sandbox.ExecCommand) == 0 || len(c.Sandbox.ReadCommand) == 0 || len(c.Sandbox.SuspendCommand) == 0 || len(c.Sandbox.ResumeCommand) == 0 || len(c.Sandbox.ResetCommand) == 0) {
		problems = append(problems, errors.New("sandbox command driver requires exec_command, read_command, suspend_command, resume_command, and reset_command"))
	}

	seen := make(map[string]struct{}, len(c.Personas))
	for i, persona := range c.Personas {
		prefix := fmt.Sprintf("personas[%d]", i)
		if persona.ID == "" {
			problems = append(problems, fmt.Errorf("%s.id is required", prefix))
		} else if _, ok := seen[persona.ID]; ok {
			problems = append(problems, fmt.Errorf("%s.id %q is duplicated", prefix, persona.ID))
		}
		seen[persona.ID] = struct{}{}
		if persona.Weight < 0 {
			problems = append(problems, fmt.Errorf("%s.weight cannot be negative", prefix))
		}
		if persona.BudgetOverride != nil {
			if err := validateBudget(prefix+".budget", *persona.BudgetOverride); err != nil {
				problems = append(problems, err)
			}
		}
	}

	for name, profile := range c.ModelProfiles {
		if strings.TrimSpace(name) == "" || profile.Provider == "" || profile.Model == "" {
			problems = append(problems, fmt.Errorf("model_profiles.%s requires provider and model", name))
		}
		if len(profile.Thinking.Allowed) == 0 && profile.Thinking.Capabilities == "" {
			problems = append(problems, fmt.Errorf("model_profiles.%s.thinking requires allowed values or a capabilities source", name))
		}
	}

	return errors.Join(problems...)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateBudget(path string, budget Budget) error {
	if budget.MaxInputTokens <= 0 || budget.MaxOutputTokens <= 0 || budget.MaxModelCalls <= 0 || budget.MaxActiveRuntime.Duration() <= 0 || budget.MaxModelSceneCommits <= 0 {
		return fmt.Errorf("%s token, call, runtime, and scene-commit limits must all be greater than zero", path)
	}
	if budget.MaxCostUSD != nil && *budget.MaxCostUSD <= 0 {
		return fmt.Errorf("%s.max_cost_usd must be greater than zero when set", path)
	}

	return nil
}

func validateTimeSpan(path string, span TimeSpan) error {
	start, err := parseClock(span.Start)
	if err != nil {
		return fmt.Errorf("%s.start: %w", path, err)
	}
	end, err := parseClock(span.End)
	if err != nil {
		return fmt.Errorf("%s.end: %w", path, err)
	}
	if start == end {
		return fmt.Errorf("%s start and end must differ", path)
	}

	return nil
}

func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("must use HH:MM: %w", err)
	}

	return parsed.Hour()*60 + parsed.Minute(), nil
}
