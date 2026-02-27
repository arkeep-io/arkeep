package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// gormAgentRepository is the GORM implementation of AgentRepository.
type gormAgentRepository struct {
	db *gorm.DB
}

// NewAgentRepository returns an AgentRepository backed by the provided *gorm.DB.
func NewAgentRepository(db *gorm.DB) AgentRepository {
	return &gormAgentRepository{db: db}
}

// Create inserts a new agent record into the database.
func (r *gormAgentRepository) Create(ctx context.Context, agent *db.Agent) error {
	if err := r.db.WithContext(ctx).Create(agent).Error; err != nil {
		return fmt.Errorf("agents: create: %w", err)
	}
	return nil
}

// GetByID retrieves an agent by its UUID. Soft-deleted agents are excluded.
// Returns ErrNotFound if no record exists.
func (r *gormAgentRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.Agent, error) {
	var agent db.Agent
	err := r.db.WithContext(ctx).First(&agent, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agents: get by id: %w", err)
	}
	return &agent, nil
}

// Update persists all fields of an existing agent record.
func (r *gormAgentRepository) Update(ctx context.Context, agent *db.Agent) error {
	result := r.db.WithContext(ctx).Save(agent)
	if result.Error != nil {
		return fmt.Errorf("agents: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStatus updates only the status and last_seen_at fields of an agent.
// This is called frequently by the agent manager on heartbeat — updating only
// two columns avoids unnecessary write amplification on the full row.
func (r *gormAgentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt time.Time) error {
	result := r.db.WithContext(ctx).
		Model(&db.Agent{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       status,
			"last_seen_at": lastSeenAt,
		})
	if result.Error != nil {
		return fmt.Errorf("agents: update status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete soft-deletes an agent by setting deleted_at. The record remains in
// the database and can be restored. Use Unscoped().Delete() for hard delete.
func (r *gormAgentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.Agent{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("agents: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns a paginated list of agents and the total count.
// Soft-deleted agents are excluded from results.
func (r *gormAgentRepository) List(ctx context.Context, opts ListOptions) ([]db.Agent, int64, error) {
	var agents []db.Agent
	var total int64

	if err := r.db.WithContext(ctx).Model(&db.Agent{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("agents: list count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at ASC").
		Find(&agents).Error; err != nil {
		return nil, 0, fmt.Errorf("agents: list: %w", err)
	}

	return agents, total, nil
}

// GetByHostname retrieves a non-deleted agent by its hostname.
// Used during agent registration to detect reconnections and avoid creating
// duplicate records when an agent reconnects without its stored ID.
// Returns ErrNotFound if no matching agent exists.
func (r *gormAgentRepository) GetByHostname(ctx context.Context, hostname string) (*db.Agent, error) {
	var agent db.Agent
	err := r.db.WithContext(ctx).First(&agent, "hostname = ?", hostname).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agents: get by hostname: %w", err)
	}
	return &agent, nil
}