package state

import (
	"database/sql"
	"fmt"
)

// CreateProject inserts a new project record.
func (d *DB) CreateProject(p *Project) error {
	_, err := d.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		p.ID, p.RootPath, p.Name, p.Slug)
	if err != nil {
		return fmt.Errorf("creating project: %w", err)
	}
	return nil
}

// GetProject retrieves a project by ID.
func (d *DB) GetProject(id string) (*Project, error) {
	row := d.QueryRow(`SELECT id, root_path, name, slug, created_at FROM projects WHERE id = ?`, id)
	return scanProject(row)
}

// GetProjectByRootPath retrieves a project by its root path.
func (d *DB) GetProjectByRootPath(rootPath string) (*Project, error) {
	row := d.QueryRow(`SELECT id, root_path, name, slug, created_at FROM projects WHERE root_path = ?`, rootPath)
	return scanProject(row)
}

// ListProjects returns all projects ordered by creation time.
func (d *DB) ListProjects() ([]Project, error) {
	rows, err := d.Query(`SELECT id, root_path, name, slug, created_at FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.RootPath, &p.Name, &p.Slug, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		p.CreatedAt = parseTime(createdAt)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func scanProject(row *sql.Row) (*Project, error) {
	var p Project
	var createdAt string
	err := row.Scan(&p.ID, &p.RootPath, &p.Name, &p.Slug, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning project: %w", err)
	}
	p.CreatedAt = parseTime(createdAt)
	return &p, nil
}
