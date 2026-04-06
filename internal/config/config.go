package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the full Axiom configuration matching Appendix A of the architecture.
type Config struct {
	Project       ProjectConfig       `toml:"project"`
	Budget        BudgetConfig        `toml:"budget"`
	Concurrency   ConcurrencyConfig   `toml:"concurrency"`
	Orchestrator  OrchestratorConfig  `toml:"orchestrator"`
	Inference     InferenceConfig     `toml:"inference"`
	BitNet        BitNetConfig        `toml:"bitnet"`
	Docker        DockerConfig        `toml:"docker"`
	Validation    ValidationConfig    `toml:"validation"`
	Security      SecurityConfig      `toml:"security"`
	Git           GitConfig           `toml:"git"`
	API           APIConfig           `toml:"api"`
	CLI           CLIConfig           `toml:"cli"`
	Observability ObservabilityConfig `toml:"observability"`
}

// InferenceConfig holds provider credentials and broker policy.
// Per Architecture Section 19.5, credentials are stored only in trusted config.
type InferenceConfig struct {
	OpenRouterAPIKey string `toml:"openrouter_api_key"`
	OpenRouterBase   string `toml:"openrouter_base_url"`
	MaxRequestsTask  int    `toml:"max_requests_per_task"`
	TokenCapPerReq   int    `toml:"token_cap_per_request"`
	TimeoutSeconds   int    `toml:"timeout_seconds"`
}

type ProjectConfig struct {
	Name string `toml:"name"`
	Slug string `toml:"slug"`
}

type BudgetConfig struct {
	MaxUSD        float64 `toml:"max_usd"`
	WarnAtPercent int     `toml:"warn_at_percent"`
}

type ConcurrencyConfig struct {
	MaxMeeseeks int `toml:"max_meeseeks"`
}

type OrchestratorConfig struct {
	Runtime             string `toml:"runtime"`
	SRSApprovalDelegate string `toml:"srs_approval_delegate"`
}

type BitNetConfig struct {
	Enabled               bool     `toml:"enabled"`
	Host                  string   `toml:"host"`
	Port                  int      `toml:"port"`
	MaxConcurrentReqs     int      `toml:"max_concurrent_requests"`
	CPUThreads            int      `toml:"cpu_threads"`
	Command               string   `toml:"command"`
	Args                  []string `toml:"args"`
	WorkingDir            string   `toml:"working_dir"`
	StartupTimeoutSeconds int      `toml:"startup_timeout_seconds"`
}

type DockerConfig struct {
	Image          string  `toml:"image"`
	TimeoutMinutes int     `toml:"timeout_minutes"`
	CPULimit       float64 `toml:"cpu_limit"`
	MemLimit       string  `toml:"mem_limit"`
	NetworkMode    string  `toml:"network_mode"`
}

type ValidationConfig struct {
	TimeoutMinutes         int               `toml:"timeout_minutes"`
	CPULimit               float64           `toml:"cpu_limit"`
	MemLimit               string            `toml:"mem_limit"`
	Network                string            `toml:"network"`
	AllowDependencyInstall bool              `toml:"allow_dependency_install"`
	SecurityScan           bool              `toml:"security_scan"`
	DependencyCacheMode    string            `toml:"dependency_cache_mode"`
	FailOnCacheMiss        bool              `toml:"fail_on_cache_miss"`
	WarmPoolEnabled        bool              `toml:"warm_pool_enabled"`
	WarmPoolSize           int               `toml:"warm_pool_size"`
	WarmColdInterval       int               `toml:"warm_cold_interval"`
	Integration            IntegrationConfig `toml:"integration"`
}

type IntegrationConfig struct {
	Enabled         bool     `toml:"enabled"`
	AllowedServices []string `toml:"allowed_services"`
	Secrets         []string `toml:"secrets"`
	NetworkEgress   []string `toml:"network_egress"`
}

type SecurityConfig struct {
	ForceLocalForSecretBearing        bool     `toml:"force_local_for_secret_bearing"`
	AllowExternalForRedactedSensitive bool     `toml:"allow_external_for_redacted_sensitive"`
	SensitivePatterns                 []string `toml:"sensitive_patterns"`
	SecurityCriticalPatterns          []string `toml:"security_critical_patterns"`
}

type GitConfig struct {
	AutoCommit   bool   `toml:"auto_commit"`
	BranchPrefix string `toml:"branch_prefix"`
}

type APIConfig struct {
	Port         int      `toml:"port"`
	RateLimitRPM int      `toml:"rate_limit_rpm"`
	AllowedIPs   []string `toml:"allowed_ips"`
}

type CLIConfig struct {
	UIMode            string `toml:"ui_mode"`
	Theme             string `toml:"theme"`
	ShowTaskRail      bool   `toml:"show_task_rail"`
	PromptSuggestions bool   `toml:"prompt_suggestions"`
	PersistSessions   bool   `toml:"persist_sessions"`
	CompactAfterMsgs  int    `toml:"compact_after_messages"`
	EditorMode        string `toml:"editor_mode"`
	ImagesEnabled     bool   `toml:"images_enabled"`
}

type ObservabilityConfig struct {
	LogPrompts     bool `toml:"log_prompts"`
	LogTokenCounts bool `toml:"log_token_counts"`
}

// Default returns a Config with architecture-specified defaults.
func Default(name, slug string) Config {
	return Config{
		Project: ProjectConfig{
			Name: name,
			Slug: slug,
		},
		Budget: BudgetConfig{
			MaxUSD:        10.00,
			WarnAtPercent: 80,
		},
		Concurrency: ConcurrencyConfig{
			MaxMeeseeks: 10,
		},
		Orchestrator: OrchestratorConfig{
			Runtime:             "claw",
			SRSApprovalDelegate: "user",
		},
		Inference: InferenceConfig{
			OpenRouterBase:  "https://openrouter.ai/api/v1",
			MaxRequestsTask: 50,
			TokenCapPerReq:  16384,
			TimeoutSeconds:  120,
		},
		BitNet: BitNetConfig{
			Enabled:               true,
			Host:                  "localhost",
			Port:                  3002,
			MaxConcurrentReqs:     4,
			CPUThreads:            4,
			Command:               "",
			Args:                  nil,
			WorkingDir:            "",
			StartupTimeoutSeconds: 30,
		},
		Docker: DockerConfig{
			Image:          "axiom-meeseeks-multi:latest",
			TimeoutMinutes: 30,
			CPULimit:       0.5,
			MemLimit:       "2g",
			NetworkMode:    "none",
		},
		Validation: ValidationConfig{
			TimeoutMinutes:         10,
			CPULimit:               1.0,
			MemLimit:               "4g",
			Network:                "none",
			AllowDependencyInstall: true,
			SecurityScan:           false,
			DependencyCacheMode:    "prefetch",
			FailOnCacheMiss:        true,
			WarmPoolEnabled:        false,
			WarmPoolSize:           3,
			WarmColdInterval:       10,
			Integration: IntegrationConfig{
				Enabled:         false,
				AllowedServices: []string{},
				Secrets:         []string{},
				NetworkEgress:   []string{},
			},
		},
		Security: SecurityConfig{
			ForceLocalForSecretBearing:        true,
			AllowExternalForRedactedSensitive: true,
			SensitivePatterns:                 []string{"*.env*", "*credentials*", "**/secrets/**"},
			SecurityCriticalPatterns:          []string{"**/auth/**", "**/crypto/**", "**/migrations/**", ".github/workflows/**"},
		},
		Git: GitConfig{
			AutoCommit:   true,
			BranchPrefix: "axiom",
		},
		API: APIConfig{
			Port:         3000,
			RateLimitRPM: 120,
			AllowedIPs:   []string{},
		},
		CLI: CLIConfig{
			UIMode:            "auto",
			Theme:             "axiom",
			ShowTaskRail:      true,
			PromptSuggestions: true,
			PersistSessions:   true,
			CompactAfterMsgs:  200,
			EditorMode:        "default",
			ImagesEnabled:     false,
		},
		Observability: ObservabilityConfig{
			LogPrompts:     false,
			LogTokenCounts: true,
		},
	}
}

// Validate checks a loaded config for required fields and valid values.
func (c *Config) Validate() error {
	var errs []error

	if c.Project.Name == "" {
		errs = append(errs, errors.New("project.name is required"))
	}
	if c.Project.Slug == "" {
		errs = append(errs, errors.New("project.slug is required"))
	}
	if c.Budget.MaxUSD < 0 {
		errs = append(errs, errors.New("budget.max_usd must be non-negative"))
	}
	if c.Budget.WarnAtPercent < 0 || c.Budget.WarnAtPercent > 100 {
		errs = append(errs, errors.New("budget.warn_at_percent must be 0-100"))
	}
	if c.Concurrency.MaxMeeseeks < 1 {
		errs = append(errs, errors.New("concurrency.max_meeseeks must be >= 1"))
	}

	validRuntimes := map[string]bool{"claw": true, "claude-code": true, "codex": true, "opencode": true}
	if !validRuntimes[c.Orchestrator.Runtime] {
		errs = append(errs, fmt.Errorf("orchestrator.runtime must be one of: claw, claude-code, codex, opencode; got %q", c.Orchestrator.Runtime))
	}
	validDelegates := map[string]bool{"user": true, "claw": true}
	if !validDelegates[c.Orchestrator.SRSApprovalDelegate] {
		errs = append(errs, fmt.Errorf("orchestrator.srs_approval_delegate must be user or claw; got %q", c.Orchestrator.SRSApprovalDelegate))
	}

	if c.Docker.TimeoutMinutes < 1 {
		errs = append(errs, errors.New("docker.timeout_minutes must be >= 1"))
	}
	if c.Docker.CPULimit <= 0 {
		errs = append(errs, errors.New("docker.cpu_limit must be > 0"))
	}
	if c.Docker.NetworkMode != "none" {
		errs = append(errs, fmt.Errorf("docker.network_mode must be \"none\"; got %q", c.Docker.NetworkMode))
	}

	if c.Validation.Network != "none" {
		errs = append(errs, fmt.Errorf("validation.network must be \"none\"; got %q", c.Validation.Network))
	}

	validUIModes := map[string]bool{"auto": true, "tui": true, "plain": true}
	if !validUIModes[c.CLI.UIMode] {
		errs = append(errs, fmt.Errorf("cli.ui_mode must be auto, tui, or plain; got %q", c.CLI.UIMode))
	}

	if c.API.Port < 1 || c.API.Port > 65535 {
		errs = append(errs, fmt.Errorf("api.port must be 1-65535; got %d", c.API.Port))
	}
	if c.BitNet.StartupTimeoutSeconds < 0 {
		errs = append(errs, fmt.Errorf("bitnet.startup_timeout_seconds must be >= 0; got %d", c.BitNet.StartupTimeoutSeconds))
	}

	return errors.Join(errs...)
}

// LoadFile reads and parses a TOML config file.
func LoadFile(path string) (*Config, error) {
	cfg, _, err := loadFileWithRaw(path)
	return cfg, err
}

func loadFileWithRaw(path string) (*Config, map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parsing raw config %s: %w", path, err)
	}
	return &cfg, raw, nil
}

// Load implements layered config: global (~/.axiom/config.toml) merged with
// project (.axiom/config.toml). Project values override global values.
// Returns default config if neither file exists.
func Load(projectRoot string) (*Config, error) {
	cfg := Default("", "")

	// Layer 1: global config
	home, err := os.UserHomeDir()
	if err == nil {
		globalPath := filepath.Join(home, ".axiom", "config.toml")
		if globalCfg, raw, err := loadFileWithRaw(globalPath); err == nil {
			cfg = mergeConfig(cfg, *globalCfg, raw)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	// Layer 2: project config
	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".axiom", "config.toml")
		if projectCfg, raw, err := loadFileWithRaw(projectPath); err == nil {
			cfg = mergeConfig(cfg, *projectCfg, raw)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	return &cfg, nil
}

// mergeConfig overlays only the fields that were explicitly present in the
// overlay TOML document, preserving defaults for omitted fields.
func mergeConfig(base, overlay Config, raw map[string]any) Config {
	dst := reflect.ValueOf(&base).Elem()
	src := reflect.ValueOf(overlay)
	mergeStruct(dst, src, raw)
	return base
}

func mergeStruct(dst, src reflect.Value, raw map[string]any) {
	if raw == nil {
		return
	}

	for i := 0; i < dst.NumField(); i++ {
		field := dst.Type().Field(i)
		key := tomlKey(field)
		rawVal, ok := raw[key]
		if !ok {
			continue
		}

		dstField := dst.Field(i)
		srcField := src.Field(i)
		if !dstField.CanSet() {
			continue
		}

		if dstField.Kind() == reflect.Struct {
			if nested, ok := rawVal.(map[string]any); ok {
				mergeStruct(dstField, srcField, nested)
				continue
			}
		}

		dstField.Set(srcField)
	}
}

func tomlKey(field reflect.StructField) string {
	tag := field.Tag.Get("toml")
	if tag == "" {
		return strings.ToLower(field.Name)
	}
	if idx := strings.Index(tag, ","); idx >= 0 {
		tag = tag[:idx]
	}
	if tag == "" {
		return strings.ToLower(field.Name)
	}
	return tag
}

// Marshal serializes a Config to TOML bytes.
func Marshal(cfg *Config) ([]byte, error) {
	return toml.Marshal(cfg)
}
