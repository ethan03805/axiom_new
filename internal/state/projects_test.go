package state

import (
	"testing"
)

func TestCreateProject(t *testing.T) {
	db := testDB(t)

	p := &Project{
		ID:       "proj-1",
		RootPath: "/home/user/myproject",
		Name:     "My Project",
		Slug:     "my-project",
	}
	if err := db.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := db.GetProject("proj-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Name != "My Project" {
		t.Errorf("Name = %q, want %q", got.Name, "My Project")
	}
	if got.Slug != "my-project" {
		t.Errorf("Slug = %q, want %q", got.Slug, "my-project")
	}
	if got.RootPath != "/home/user/myproject" {
		t.Errorf("RootPath = %q, want %q", got.RootPath, "/home/user/myproject")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestGetProjectNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetProject("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetProjectByRootPath(t *testing.T) {
	db := testDB(t)

	p := &Project{
		ID:       "proj-1",
		RootPath: "/home/user/myproject",
		Name:     "My Project",
		Slug:     "my-project",
	}
	if err := db.CreateProject(p); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetProjectByRootPath("/home/user/myproject")
	if err != nil {
		t.Fatalf("GetProjectByRootPath: %v", err)
	}
	if got.ID != "proj-1" {
		t.Errorf("ID = %q, want %q", got.ID, "proj-1")
	}
}

func TestGetProjectByRootPathNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetProjectByRootPath("/nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListProjects(t *testing.T) {
	db := testDB(t)

	for i, name := range []string{"Alpha", "Beta", "Gamma"} {
		p := &Project{
			ID:       "proj-" + name,
			RootPath: "/tmp/" + name,
			Name:     name,
			Slug:     name,
		}
		_ = i
		if err := db.CreateProject(p); err != nil {
			t.Fatal(err)
		}
	}

	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 3 {
		t.Errorf("len = %d, want 3", len(projects))
	}
}

func TestCreateProjectDuplicateRootPath(t *testing.T) {
	db := testDB(t)

	p1 := &Project{ID: "proj-1", RootPath: "/same/path", Name: "A", Slug: "a"}
	if err := db.CreateProject(p1); err != nil {
		t.Fatal(err)
	}

	p2 := &Project{ID: "proj-2", RootPath: "/same/path", Name: "B", Slug: "b"}
	err := db.CreateProject(p2)
	if err == nil {
		t.Error("expected error on duplicate root_path")
	}
}
