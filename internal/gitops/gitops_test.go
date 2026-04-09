package gitops

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temporary git repo with an initial commit on "main".
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "user.email", "test@test.com")

	writeFile(t, dir, "README.md", "# Test\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial commit")

	return dir
}

// gitRun executes a git command in dir, fataling on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile writes content to a file inside dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testManager() *Manager {
	return New(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

// =====================================================================
// CurrentBranch
// =====================================================================

func TestCurrentBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	branch, err := mgr.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected 'main', got %q", branch)
	}
}

// =====================================================================
// CreateBranch (create only, no checkout)
// =====================================================================

func TestCreateBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	if err := mgr.CreateBranch(dir, "axiom/test-project"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// Should still be on main
	branch, _ := mgr.CurrentBranch(dir)
	if branch != "main" {
		t.Errorf("CreateBranch should not switch branches; on %q", branch)
	}

	// Branch should exist
	exists, _ := mgr.BranchExists(dir, "axiom/test-project")
	if !exists {
		t.Error("created branch should exist")
	}
}

func TestCreateBranch_AlreadyExists(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateBranch(dir, "axiom/dup")
	err := mgr.CreateBranch(dir, "axiom/dup")
	if err == nil {
		t.Fatal("expected error creating duplicate branch")
	}
}

// =====================================================================
// CreateAndCheckoutBranch
// =====================================================================

func TestCreateAndCheckoutBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	if err := mgr.CreateAndCheckoutBranch(dir, "axiom/test-project"); err != nil {
		t.Fatalf("CreateAndCheckoutBranch: %v", err)
	}

	branch, _ := mgr.CurrentBranch(dir)
	if branch != "axiom/test-project" {
		t.Errorf("expected 'axiom/test-project', got %q", branch)
	}
}

func TestCreateAndCheckoutBranch_AlreadyExists(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/dup")
	mgr.CheckoutBranch(dir, "main")

	err := mgr.CreateAndCheckoutBranch(dir, "axiom/dup")
	if err == nil {
		t.Fatal("expected error creating existing branch")
	}
}

// =====================================================================
// CheckoutBranch
// =====================================================================

func TestCheckoutBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateBranch(dir, "feature")
	if err := mgr.CheckoutBranch(dir, "feature"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}

	branch, _ := mgr.CurrentBranch(dir)
	if branch != "feature" {
		t.Errorf("expected 'feature', got %q", branch)
	}
}

func TestCheckoutBranch_NonExistent(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	err := mgr.CheckoutBranch(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error checking out nonexistent branch")
	}
}

// =====================================================================
// BranchExists
// =====================================================================

func TestBranchExists(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	exists, err := mgr.BranchExists(dir, "main")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !exists {
		t.Error("main should exist")
	}

	exists, err = mgr.BranchExists(dir, "nonexistent")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if exists {
		t.Error("nonexistent should not exist")
	}
}

// =====================================================================
// CurrentHEAD / Snapshot
// =====================================================================

func TestCurrentHEAD(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	sha, err := mgr.CurrentHEAD(dir)
	if err != nil {
		t.Fatalf("CurrentHEAD: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("expected 40-char SHA, got %q (len %d)", sha, len(sha))
	}
}

func TestSnapshot(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	snap, err := mgr.Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	head, _ := mgr.CurrentHEAD(dir)
	if snap != head {
		t.Errorf("Snapshot should equal CurrentHEAD: %q vs %q", snap, head)
	}
}

// =====================================================================
// IsDirty / ValidateClean
// =====================================================================

func TestIsDirty_CleanRepo(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	dirty, err := mgr.IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if dirty {
		t.Error("expected clean repo")
	}
}

func TestIsDirty_UntrackedFile(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "untracked.txt", "data")

	dirty, err := mgr.IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if !dirty {
		t.Error("expected dirty repo (untracked file)")
	}
}

func TestIsDirty_StagedChanges(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "staged.txt", "data")
	gitRun(t, dir, "add", "staged.txt")

	dirty, err := mgr.IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if !dirty {
		t.Error("expected dirty repo (staged changes)")
	}
}

func TestIsDirty_ModifiedTrackedFile(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "README.md", "modified content\n")

	dirty, err := mgr.IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if !dirty {
		t.Error("expected dirty repo (modified tracked file)")
	}
}

func TestValidateClean_Clean(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	if err := mgr.ValidateClean(dir); err != nil {
		t.Errorf("expected no error for clean repo: %v", err)
	}
}

func TestValidateClean_Dirty(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "dirty.txt", "dirty")

	err := mgr.ValidateClean(dir)
	if err == nil {
		t.Error("expected error for dirty repo")
	}
	if !strings.Contains(err.Error(), "dirty") && !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error should mention dirty/uncommitted state: %v", err)
	}
}

// =====================================================================
// FormatCommitMessage (pure function)
// =====================================================================

func TestFormatCommitMessage(t *testing.T) {
	info := CommitInfo{
		TaskTitle:     "Add user auth",
		TaskID:        "task-001",
		SRSRefs:       []string{"FR-002"},
		MeeseeksModel: "anthropic/claude-4-sonnet",
		ReviewerModel: "openai/gpt-4o",
		AttemptNumber: 1,
		MaxAttempts:   3,
		CostUSD:       0.05,
		BaseSnapshot:  "def456a",
	}

	msg := FormatCommitMessage(info)

	expected := "[axiom] Add user auth\n\nTask: task-001\nSRS Refs: FR-002\nMeeseeks Model: anthropic/claude-4-sonnet\nReviewer Model: openai/gpt-4o\nAttempt: 1/3\nCost: $0.0500\nBase Snapshot: def456a"

	if msg != expected {
		t.Errorf("unexpected commit message:\ngot:\n%s\nwant:\n%s", msg, expected)
	}
}

func TestFormatCommitMessage_MultipleSRSRefs(t *testing.T) {
	info := CommitInfo{
		TaskTitle:     "Implement feature",
		TaskID:        "task-042",
		SRSRefs:       []string{"FR-001", "AC-003", "NFR-005"},
		MeeseeksModel: "anthropic/claude-4-sonnet",
		ReviewerModel: "openai/gpt-4o",
		AttemptNumber: 2,
		MaxAttempts:   3,
		CostUSD:       0.0234,
		BaseSnapshot:  "abc123d",
	}

	msg := FormatCommitMessage(info)

	if !strings.Contains(msg, "SRS Refs: FR-001, AC-003, NFR-005") {
		t.Error("should join multiple SRS refs with commas")
	}
}

func TestFormatCommitMessage_MatchesArchitectureFormat(t *testing.T) {
	// Exact example from Architecture Section 23.2
	info := CommitInfo{
		TaskTitle:     "Implement feature X",
		TaskID:        "task-042",
		SRSRefs:       []string{"FR-001", "AC-003"},
		MeeseeksModel: "anthropic/claude-4-sonnet",
		ReviewerModel: "openai/gpt-4o",
		AttemptNumber: 2,
		MaxAttempts:   3,
		CostUSD:       0.0234,
		BaseSnapshot:  "abc123d",
	}

	msg := FormatCommitMessage(info)

	// Verify all required fields from the architecture
	checks := []string{
		"[axiom] Implement feature X",
		"Task: task-042",
		"SRS Refs: FR-001, AC-003",
		"Meeseeks Model: anthropic/claude-4-sonnet",
		"Reviewer Model: openai/gpt-4o",
		"Attempt: 2/3",
		"Cost: $0.0234",
		"Base Snapshot: abc123d",
	}
	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Errorf("commit message missing %q\ngot:\n%s", c, msg)
		}
	}

	// First line should be the subject
	lines := strings.SplitN(msg, "\n", 2)
	if lines[0] != "[axiom] Implement feature X" {
		t.Errorf("first line should be subject, got %q", lines[0])
	}

	// Second line should be blank (standard git format)
	bodyLines := strings.Split(msg, "\n")
	if len(bodyLines) < 3 || bodyLines[1] != "" {
		t.Error("second line should be blank (git convention)")
	}
}

// =====================================================================
// AddFiles
// =====================================================================

func TestAddFiles(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "a.go", "package a\n")
	writeFile(t, dir, "b.go", "package b\n")

	if err := mgr.AddFiles(dir, []string{"a.go", "b.go"}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}

	// Verify files are staged
	staged, err := mgr.DiffStaged(dir)
	if err != nil {
		t.Fatalf("DiffStaged: %v", err)
	}
	if !strings.Contains(staged, "a.go") || !strings.Contains(staged, "b.go") {
		t.Errorf("both files should be staged, diff:\n%s", staged)
	}
}

func TestAddFiles_Empty(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	err := mgr.AddFiles(dir, []string{})
	if err == nil {
		t.Error("expected error when adding empty file list")
	}
}

// =====================================================================
// Commit
// =====================================================================

func TestCommit(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "new.go", "package main\n")
	gitRun(t, dir, "add", ".")

	sha, err := mgr.Commit(dir, "test commit message")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("expected 40-char SHA, got %q", sha)
	}

	// Verify commit message
	msg := gitRun(t, dir, "log", "-1", "--format=%s")
	if msg != "test commit message" {
		t.Errorf("unexpected commit subject: %q", msg)
	}
}

func TestCommit_NothingStaged(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	_, err := mgr.Commit(dir, "empty commit")
	if err == nil {
		t.Error("expected error committing with nothing staged")
	}
}

// =====================================================================
// CommitTask (integration: add + commit with architecture format)
// =====================================================================

func TestCommitTask(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "feature.go", "package main\n")

	info := CommitInfo{
		TaskTitle:     "Implement feature X",
		TaskID:        "task-042",
		SRSRefs:       []string{"FR-001", "AC-003"},
		MeeseeksModel: "anthropic/claude-4-sonnet",
		ReviewerModel: "openai/gpt-4o",
		AttemptNumber: 2,
		MaxAttempts:   3,
		CostUSD:       0.0234,
		BaseSnapshot:  "abc123d",
	}

	sha, err := mgr.CommitTask(dir, info, []string{"feature.go"})
	if err != nil {
		t.Fatalf("CommitTask: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("expected 40-char SHA, got %q", sha)
	}

	// Verify the full commit message
	msg := gitRun(t, dir, "log", "-1", "--format=%B")
	if !strings.HasPrefix(msg, "[axiom] Implement feature X") {
		t.Errorf("commit message subject wrong:\n%s", msg)
	}
	if !strings.Contains(msg, "Task: task-042") {
		t.Error("commit message missing Task ID")
	}
	if !strings.Contains(msg, "SRS Refs: FR-001, AC-003") {
		t.Error("commit message missing SRS refs")
	}
	if !strings.Contains(msg, "Meeseeks Model: anthropic/claude-4-sonnet") {
		t.Error("commit message missing Meeseeks model")
	}
	if !strings.Contains(msg, "Reviewer Model: openai/gpt-4o") {
		t.Error("commit message missing Reviewer model")
	}
	if !strings.Contains(msg, "Attempt: 2/3") {
		t.Error("commit message missing attempt info")
	}
	if !strings.Contains(msg, "Cost: $0.0234") {
		t.Error("commit message missing cost")
	}
	if !strings.Contains(msg, "Base Snapshot: abc123d") {
		t.Error("commit message missing base snapshot")
	}
}

func TestCommitTask_NoFiles(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	info := CommitInfo{TaskTitle: "noop", TaskID: "t-1"}
	_, err := mgr.CommitTask(dir, info, nil)
	if err == nil {
		t.Error("expected error when no files provided")
	}
}

// =====================================================================
// Diff
// =====================================================================

func TestDiff(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	baseSHA, _ := mgr.CurrentHEAD(dir)

	writeFile(t, dir, "new.txt", "hello\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "add file")

	headSHA, _ := mgr.CurrentHEAD(dir)

	diff, err := mgr.Diff(dir, baseSHA, headSHA)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "new.txt") {
		t.Errorf("diff should mention new.txt:\n%s", diff)
	}
	if !strings.Contains(diff, "hello") {
		t.Errorf("diff should contain file content:\n%s", diff)
	}
}

func TestDiff_NoChanges(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	sha, _ := mgr.CurrentHEAD(dir)
	diff, err := mgr.Diff(dir, sha, sha)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "" {
		t.Errorf("diff of same SHA should be empty, got:\n%s", diff)
	}
}

// =====================================================================
// DiffStaged
// =====================================================================

func TestDiffStaged(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "staged.go", "package staged\n")
	gitRun(t, dir, "add", "staged.go")

	diff, err := mgr.DiffStaged(dir)
	if err != nil {
		t.Fatalf("DiffStaged: %v", err)
	}
	if !strings.Contains(diff, "staged.go") {
		t.Errorf("staged diff should mention staged.go:\n%s", diff)
	}
}

func TestDiffStaged_NothingStaged(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	diff, err := mgr.DiffStaged(dir)
	if err != nil {
		t.Fatalf("DiffStaged: %v", err)
	}
	if diff != "" {
		t.Errorf("no staged changes, expected empty diff, got:\n%s", diff)
	}
}

// =====================================================================
// DiffWorkBranch
// =====================================================================

func TestDiffWorkBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/test")
	writeFile(t, dir, "feature.go", "package feature\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "add feature")

	diff, err := mgr.DiffWorkBranch(dir, "main", "axiom/test")
	if err != nil {
		t.Fatalf("DiffWorkBranch: %v", err)
	}
	if !strings.Contains(diff, "feature.go") {
		t.Errorf("diff should contain feature.go:\n%s", diff)
	}
}

func TestDiffWorkBranch_NoDifference(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/test")

	diff, err := mgr.DiffWorkBranch(dir, "main", "axiom/test")
	if err != nil {
		t.Fatalf("DiffWorkBranch: %v", err)
	}
	if diff != "" {
		t.Errorf("expected empty diff for identical branches, got:\n%s", diff)
	}
}

// =====================================================================
// SetupWorkBranch
// =====================================================================

func TestSetupWorkBranch_New(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	err := mgr.SetupWorkBranch(dir, "main", "axiom/my-project")
	if err != nil {
		t.Fatalf("SetupWorkBranch: %v", err)
	}

	branch, _ := mgr.CurrentBranch(dir)
	if branch != "axiom/my-project" {
		t.Errorf("expected 'axiom/my-project', got %q", branch)
	}
}

func TestSetupWorkBranch_ExistingBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	// Create branch and make a commit
	mgr.CreateAndCheckoutBranch(dir, "axiom/existing")
	writeFile(t, dir, "work.go", "package work\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "prior work")
	mgr.CheckoutBranch(dir, "main")

	// SetupWorkBranch should detect existing and checkout
	err := mgr.SetupWorkBranch(dir, "main", "axiom/existing")
	if err != nil {
		t.Fatalf("SetupWorkBranch (existing): %v", err)
	}

	branch, _ := mgr.CurrentBranch(dir)
	if branch != "axiom/existing" {
		t.Errorf("expected 'axiom/existing', got %q", branch)
	}

	// Prior work should be present
	if _, err := os.Stat(filepath.Join(dir, "work.go")); os.IsNotExist(err) {
		t.Error("prior work should be present on resumed branch")
	}
}

func TestSetupWorkBranch_DirtyRepoFails(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	writeFile(t, dir, "dirty.txt", "dirty")

	err := mgr.SetupWorkBranch(dir, "main", "axiom/test")
	if err == nil {
		t.Error("expected error setting up work branch on dirty repo")
	}
}

// =====================================================================
// CancelCleanup
// =====================================================================

func TestCancelCleanup_RevertsUncommittedChanges(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/test")

	// Make uncommitted changes (both staged and unstaged)
	writeFile(t, dir, "staged.txt", "staged data")
	gitRun(t, dir, "add", "staged.txt")
	writeFile(t, dir, "untracked.txt", "untracked data")

	err := mgr.CancelCleanup(dir, "main")
	if err != nil {
		t.Fatalf("CancelCleanup: %v", err)
	}

	// Should be back on main
	branch, _ := mgr.CurrentBranch(dir)
	if branch != "main" {
		t.Errorf("expected main after cancel, got %q", branch)
	}

	// Uncommitted files should not exist on main
	for _, f := range []string{"staged.txt", "untracked.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist after cancel cleanup", f)
		}
	}
}

func TestCancelCleanup_PreservesCommittedWork(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/test")

	// Commit some work
	writeFile(t, dir, "committed.go", "package committed\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "committed work")

	// Add uncommitted changes on top
	writeFile(t, dir, "uncommitted.txt", "uncommitted")

	err := mgr.CancelCleanup(dir, "main")
	if err != nil {
		t.Fatalf("CancelCleanup: %v", err)
	}

	// Work branch should still exist with committed work
	exists, _ := mgr.BranchExists(dir, "axiom/test")
	if !exists {
		t.Error("work branch should still exist after cancel")
	}

	// Switch back to verify committed work is preserved
	mgr.CheckoutBranch(dir, "axiom/test")
	if _, err := os.Stat(filepath.Join(dir, "committed.go")); os.IsNotExist(err) {
		t.Error("committed.go should be preserved on work branch")
	}
	if _, err := os.Stat(filepath.Join(dir, "uncommitted.txt")); !os.IsNotExist(err) {
		t.Error("uncommitted.txt should be cleaned from work branch")
	}
}

func TestCancelCleanup_CleanWorkBranch(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/test")

	// No uncommitted changes
	err := mgr.CancelCleanup(dir, "main")
	if err != nil {
		t.Fatalf("CancelCleanup on clean branch: %v", err)
	}

	branch, _ := mgr.CurrentBranch(dir)
	if branch != "main" {
		t.Errorf("expected main after cancel, got %q", branch)
	}
}

// =====================================================================
// DetectBaseBranch
// =====================================================================

// initTestRepoOnBranch creates a temporary git repo with an initial commit
// on the given branch name. Used by DetectBaseBranch tests to set up repos
// with non-default trunk names without depending on system git config.
func initTestRepoOnBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()

	gitRun(t, dir, "init", "-b", branch)
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "user.email", "test@test.com")

	writeFile(t, dir, "README.md", "# Test\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial commit")

	return dir
}

func TestDetectBaseBranch_RepoOnMain(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	// Clear any inherited init.defaultBranch so the fallback tier is
	// exercised deterministically. We want the "current branch" rule to
	// fire, not an ambient config.
	_ = exec.Command("git", "-C", dir, "config", "--local", "--unset-all", "init.defaultBranch").Run()

	branch, err := mgr.DetectBaseBranch(dir)
	if err != nil {
		t.Fatalf("DetectBaseBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected 'main', got %q", branch)
	}
}

func TestDetectBaseBranch_RepoOnMaster(t *testing.T) {
	dir := initTestRepoOnBranch(t, "master")
	mgr := testManager()

	// Ensure no init.defaultBranch is set locally so we exercise the
	// "current branch" rule rather than the config rule.
	_ = exec.Command("git", "-C", dir, "config", "--local", "--unset-all", "init.defaultBranch").Run()

	branch, err := mgr.DetectBaseBranch(dir)
	if err != nil {
		t.Fatalf("DetectBaseBranch: %v", err)
	}
	if branch != "master" {
		t.Errorf("expected 'master', got %q", branch)
	}
}

func TestDetectBaseBranch_InitDefaultBranchOverride(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	// Create a non-standard trunk branch and set init.defaultBranch to it.
	// Then check out a feature branch — DetectBaseBranch should still return
	// the configured default because that rule has the highest priority.
	if err := mgr.CreateBranch(dir, "trunk"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	gitRun(t, dir, "config", "--local", "init.defaultBranch", "trunk")
	if err := mgr.CreateAndCheckoutBranch(dir, "feature/widget"); err != nil {
		t.Fatalf("CreateAndCheckoutBranch: %v", err)
	}

	branch, err := mgr.DetectBaseBranch(dir)
	if err != nil {
		t.Fatalf("DetectBaseBranch: %v", err)
	}
	if branch != "trunk" {
		t.Errorf("expected 'trunk' (init.defaultBranch wins), got %q", branch)
	}
}

func TestDetectBaseBranch_InitDefaultBranchMissingFallsBack(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	// init.defaultBranch points at a branch that doesn't exist locally. The
	// detector should ignore it and fall through to the current-branch rule.
	gitRun(t, dir, "config", "--local", "init.defaultBranch", "does-not-exist")

	branch, err := mgr.DetectBaseBranch(dir)
	if err != nil {
		t.Fatalf("DetectBaseBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected fallback to current branch 'main', got %q", branch)
	}
}

func TestDetectBaseBranch_NoBranches(t *testing.T) {
	dir := t.TempDir()
	// Initialise a completely empty repo: no initial commit, no branches.
	gitRun(t, dir, "init", "-b", "trunk")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "user.email", "test@test.com")
	// Do not commit anything, so refs/heads/trunk does not yet exist,
	// CurrentBranch returns "trunk" via symbolic-ref but rev-parse returns
	// HEAD because the branch is unborn. DetectBaseBranch should error out.

	mgr := testManager()
	_, err := mgr.DetectBaseBranch(dir)
	if err == nil {
		t.Fatal("expected error when repo has no branches, got nil")
	}
	if !strings.Contains(err.Error(), "could not detect base branch") {
		t.Errorf("expected actionable error message, got: %v", err)
	}
}

// =====================================================================
// Exit Criteria: Work branch creation is deterministic
// =====================================================================

func TestWorkBranch_Deterministic(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	branchName := "axiom/my-project"
	mgr.CreateAndCheckoutBranch(dir, branchName)

	branch, _ := mgr.CurrentBranch(dir)
	if branch != branchName {
		t.Errorf("branch name should be deterministic: expected %q, got %q", branchName, branch)
	}

	// Work branch starts at same HEAD as base
	mgr.CheckoutBranch(dir, "main")
	mainHEAD, _ := mgr.CurrentHEAD(dir)
	mgr.CheckoutBranch(dir, branchName)
	workHEAD, _ := mgr.CurrentHEAD(dir)

	if mainHEAD != workHEAD {
		t.Error("work branch should start at same HEAD as base branch")
	}
}

// =====================================================================
// Exit Criteria: Commits follow the architecture format
// =====================================================================

func TestCommitFormat_ArchitectureCompliance(t *testing.T) {
	dir := initTestRepo(t)
	mgr := testManager()

	mgr.CreateAndCheckoutBranch(dir, "axiom/test")

	writeFile(t, dir, "impl.go", "package impl\n")

	info := CommitInfo{
		TaskTitle:     "Implement user service",
		TaskID:        "task-007",
		SRSRefs:       []string{"FR-010"},
		MeeseeksModel: "anthropic/claude-4-sonnet",
		ReviewerModel: "deepseek/deepseek-chat",
		AttemptNumber: 1,
		MaxAttempts:   3,
		CostUSD:       0.1234,
		BaseSnapshot:  "abcdef1",
	}

	_, err := mgr.CommitTask(dir, info, []string{"impl.go"})
	if err != nil {
		t.Fatalf("CommitTask: %v", err)
	}

	msg := gitRun(t, dir, "log", "-1", "--format=%B")

	// Architecture Section 23.2: [axiom] <task-title> on first line
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	if lines[0] != "[axiom] Implement user service" {
		t.Errorf("subject line: %q", lines[0])
	}
	// Blank line after subject
	if len(lines) < 2 || lines[1] != "" {
		t.Error("missing blank line after subject")
	}
	// All metadata fields present
	body := strings.Join(lines[2:], "\n")
	required := map[string]string{
		"Task":           "task-007",
		"SRS Refs":       "FR-010",
		"Meeseeks Model": "anthropic/claude-4-sonnet",
		"Reviewer Model":  "deepseek/deepseek-chat",
		"Attempt":         "1/3",
		"Cost":            "$0.1234",
		"Base Snapshot":   "abcdef1",
	}
	for field, value := range required {
		expected := fmt.Sprintf("%s: %s", field, value)
		if !strings.Contains(body, expected) {
			t.Errorf("missing in body: %q\nbody:\n%s", expected, body)
		}
	}
}

// =====================================================================
// No push/remote operations (design constraint from Section 23.4)
// This is verified by the fact that Manager has no Push, Fetch, Pull,
// or remote-related methods. A compilation test is implicit.
// =====================================================================
