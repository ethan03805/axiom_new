package cli

import (
	"errors"
	"fmt"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/state"
)

// findProjectID looks up the project record by the app's root path.
func findProjectID(application *app.App) (string, error) {
	proj, err := application.DB.GetProjectByRootPath(application.ProjectRoot)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", fmt.Errorf("no project found at %s (run 'axiom init' first)", application.ProjectRoot)
		}
		return "", fmt.Errorf("looking up project: %w", err)
	}
	return proj.ID, nil
}

// findActiveRun returns the currently active run for a project, or an error
// if no active run exists.
func findActiveRun(application *app.App, projectID string) (*state.ProjectRun, error) {
	run, err := application.DB.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, fmt.Errorf("no active run (start one with 'axiom run \"<prompt>\"')")
		}
		return nil, fmt.Errorf("getting active run: %w", err)
	}
	return run, nil
}

// openApp is a helper for CLI commands that need the full application context.
func openApp(verbose *bool) (*app.App, error) {
	log := app.NewLogger(*verbose)
	return app.Open(log)
}
