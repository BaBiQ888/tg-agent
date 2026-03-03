package models

import (
	"context"
	"time"
)

// GetAllRoles returns all roles from the database
func GetAllRoles(ctx context.Context) ([]Role, error) {
	var roles []Role
	rows, err := DB().Query(ctx, `
		SELECT id, name, description, personality, skills, constraints, 
		       output_format, native_language, target, gender, is_public, 
		       owner_id, created_at, updated_at, deleted_at
		FROM agent_roles 
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var role Role
		err := rows.Scan(&role.ID, &role.Name, &role.Description, &role.Personality,
			&role.Skills, &role.Constraints, &role.OutputFormat, &role.NativeLanguage,
			&role.Target, &role.Gender, &role.IsPublic, &role.OwnerID,
			&role.CreatedAt, &role.UpdatedAt, &role.DeletedAt)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

type Role struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Personality    string     `json:"personality"`
	Skills         string     `json:"skills"`
	Constraints    string     `json:"constraints"`
	OutputFormat   string     `json:"output_format"`
	NativeLanguage string     `json:"native_language"`
	Target         string     `json:"target"`
	Gender         string     `json:"gender"`
	IsPublic       int16      `json:"is_public"`
	OwnerID        int64      `json:"owner_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
}
