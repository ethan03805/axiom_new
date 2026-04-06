package cli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/bitnet"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/models"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/spf13/cobra"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testApp(t *testing.T) *app.App {
	t.Helper()
	db := testDB(t)
	log := testLogger()
	dir := t.TempDir()
	cfg := config.Default("test-project", "test-project")

	registry := models.NewRegistry(db, log)
	if err := registry.RefreshShipped(); err != nil {
		t.Fatal(err)
	}

	bitnetSvc := bitnet.NewService(&cfg)

	eng, err := engine.New(engine.Options{
		Config:  &cfg,
		DB:      db,
		RootDir: dir,
		Log:     log,
		Models:  models.NewRegistryAdapter(registry),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Stop() })

	return &app.App{
		Config:      &cfg,
		DB:          db,
		Engine:      eng,
		Registry:    registry,
		BitNet:      bitnetSvc,
		ProjectRoot: dir,
		Log:         log,
	}
}

// testAppWithProject creates a test app with a project record already seeded.
func testAppWithProject(t *testing.T) (*app.App, *state.Project) {
	t.Helper()
	application := testApp(t)

	proj := &state.Project{
		ID:       "proj-test",
		RootPath: application.ProjectRoot,
		Name:     "test-project",
		Slug:     "test-project",
	}
	if err := application.DB.CreateProject(proj); err != nil {
		t.Fatal(err)
	}
	return application, proj
}

// testAppWithActiveRun creates a test app with a project and an active run.
func testAppWithActiveRun(t *testing.T) (*app.App, *state.Project, *state.ProjectRun) {
	t.Helper()
	application, proj := testAppWithProject(t)

	run, err := application.Engine.CreateRun(engine.RunOptions{
		ProjectID:  proj.ID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Transition to active: draft_srs -> awaiting_srs_approval -> active
	if err := application.DB.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := application.DB.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	// Re-read to get updated state
	run, err = application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}

	return application, proj, run
}

// executeCmd runs a cobra command with the given args and captures stdout.
func executeCmd(cmd *cobra.Command, args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// --- Command registration tests ---

func TestAllSection27CommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	// Build a map of command names (including subcommands)
	cmdNames := make(map[string]bool)
	for _, cmd := range commands {
		cmdNames[cmd.Name()] = true
	}

	// Section 27 command groups that must exist
	required := []string{
		"run", "pause", "resume", "cancel", "export",
		"models", "bitnet", "index",
		"session", "api", "tunnel", "skill", "tui", "doctor",
	}

	for _, name := range required {
		if !cmdNames[name] {
			t.Errorf("missing required command: %q", name)
		}
	}
}

func TestModelsSubcommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	var modelsCmd *cobra.Command
	for _, cmd := range commands {
		if cmd.Name() == "models" {
			modelsCmd = cmd
			break
		}
	}
	if modelsCmd == nil {
		t.Fatal("models command not found")
	}

	subNames := make(map[string]bool)
	for _, sub := range modelsCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range []string{"refresh", "list", "info"} {
		if !subNames[name] {
			t.Errorf("missing models subcommand: %q", name)
		}
	}
}

func TestBitnetSubcommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	var bitnetCmd *cobra.Command
	for _, cmd := range commands {
		if cmd.Name() == "bitnet" {
			bitnetCmd = cmd
			break
		}
	}
	if bitnetCmd == nil {
		t.Fatal("bitnet command not found")
	}

	subNames := make(map[string]bool)
	for _, sub := range bitnetCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range []string{"start", "stop", "status", "models"} {
		if !subNames[name] {
			t.Errorf("missing bitnet subcommand: %q", name)
		}
	}
}

func TestIndexSubcommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	var indexCmd *cobra.Command
	for _, cmd := range commands {
		if cmd.Name() == "index" {
			indexCmd = cmd
			break
		}
	}
	if indexCmd == nil {
		t.Fatal("index command not found")
	}

	subNames := make(map[string]bool)
	for _, sub := range indexCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range []string{"refresh", "query"} {
		if !subNames[name] {
			t.Errorf("missing index subcommand: %q", name)
		}
	}
}

func TestAPISubcommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	var apiCmd *cobra.Command
	for _, cmd := range commands {
		if cmd.Name() == "api" {
			apiCmd = cmd
			break
		}
	}
	if apiCmd == nil {
		t.Fatal("api command not found")
	}

	subNames := make(map[string]bool)
	for _, sub := range apiCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range []string{"start", "stop", "token"} {
		if !subNames[name] {
			t.Errorf("missing api subcommand: %q", name)
		}
	}
}

func TestSessionSubcommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	var sessionCmd *cobra.Command
	for _, cmd := range commands {
		if cmd.Name() == "session" {
			sessionCmd = cmd
			break
		}
	}
	if sessionCmd == nil {
		t.Fatal("session command not found")
	}

	subNames := make(map[string]bool)
	for _, sub := range sessionCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range []string{"list", "resume", "export"} {
		if !subNames[name] {
			t.Errorf("missing session subcommand: %q", name)
		}
	}
}

func TestTunnelSubcommandsExist(t *testing.T) {
	verbose := false
	commands := Commands(&verbose)

	var tunnelCmd *cobra.Command
	for _, cmd := range commands {
		if cmd.Name() == "tunnel" {
			tunnelCmd = cmd
			break
		}
	}
	if tunnelCmd == nil {
		t.Fatal("tunnel command not found")
	}

	subNames := make(map[string]bool)
	for _, sub := range tunnelCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range []string{"start", "stop"} {
		if !subNames[name] {
			t.Errorf("missing tunnel subcommand: %q", name)
		}
	}
}

