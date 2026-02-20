package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// gormPolicyRepository is the GORM implementation of PolicyRepository.
type gormPolicyRepository struct {
	db *gorm.DB
}

// NewPolicyRepository returns a PolicyRepository backed by the provided *gorm.DB.
func NewPolicyRepository(db *gorm.DB) PolicyRepository {
	return &gormPolicyRepository{db: db}
}

// Create inserts a new policy record into the database.
// RepoPassword is automatically encrypted by EncryptedString.Value()
// before being written to the database.
func (r *gormPolicyRepository) Create(ctx context.Context, policy *db.Policy) error {
	if err := r.db.WithContext(ctx).Create(policy).Error; err != nil {
		return fmt.Errorf("policies: create: %w", err)
	}
	return nil
}

// GetByID retrieves a policy by its UUID. Soft-deleted policies are excluded.
// Returns ErrNotFound if no record exists.
func (r *gormPolicyRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.Policy, error) {
	var policy db.Policy
	err := r.db.WithContext(ctx).First(&policy, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("policies: get by id: %w", err)
	}
	return &policy, nil
}

// GetByIDWithDestinations retrieves a policy with its associated destinations
// preloaded. Use this when you need to access policy.Destinations in the
// same call to avoid N+1 queries.
func (r *gormPolicyRepository) GetByIDWithDestinations(ctx context.Context, id uuid.UUID) (*db.Policy, error) {
	var policy db.Policy
	err := r.db.WithContext(ctx).
		Preload("Destinations").
		Preload("Destinations.Destination").
		First(&policy, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("policies: get by id with destinations: %w", err)
	}
	return &policy, nil
}

// Update persists all fields of an existing policy record.
func (r *gormPolicyRepository) Update(ctx context.Context, policy *db.Policy) error {
	result := r.db.WithContext(ctx).Save(policy)
	if result.Error != nil {
		return fmt.Errorf("policies: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete soft-deletes a policy by setting deleted_at. Associated
// policy_destinations are cascade-deleted automatically by the database.
func (r *gormPolicyRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.Policy{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("policies: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns a paginated list of policies and the total count.
// Soft-deleted policies are excluded from results.
func (r *gormPolicyRepository) List(ctx context.Context, opts ListOptions) ([]db.Policy, int64, error) {
	var policies []db.Policy
	var total int64

	if err := r.db.WithContext(ctx).Model(&db.Policy{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("policies: list count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at ASC").
		Find(&policies).Error; err != nil {
		return nil, 0, fmt.Errorf("policies: list: %w", err)
	}

	return policies, total, nil
}

// ListByAgent returns all non-deleted policies associated with a given agent.
// Used by the scheduler to load policies for a specific agent.
func (r *gormPolicyRepository) ListByAgent(ctx context.Context, agentID uuid.UUID) ([]db.Policy, error) {
	var policies []db.Policy
	if err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("created_at ASC").
		Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("policies: list by agent: %w", err)
	}
	return policies, nil
}

// ListEnabled returns all non-deleted, enabled policies.
// Used by the scheduler at startup to register all active cron jobs.
func (r *gormPolicyRepository) ListEnabled(ctx context.Context) ([]db.Policy, error) {
	var policies []db.Policy
	if err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("created_at ASC").
		Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("policies: list enabled: %w", err)
	}
	return policies, nil
}

// UpdateSchedule updates last_run_at and next_run_at for a policy.
// Called by the scheduler after each job execution to keep schedule
// metadata in sync without updating the full policy record.
func (r *gormPolicyRepository) UpdateSchedule(ctx context.Context, id uuid.UUID, lastRunAt, nextRunAt time.Time) error {
	result := r.db.WithContext(ctx).
		Model(&db.Policy{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"last_run_at": lastRunAt,
			"next_run_at": nextRunAt,
		})
	if result.Error != nil {
		return fmt.Errorf("policies: update schedule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------------
// PolicyDestination
// -----------------------------------------------------------------------------

// AddDestination adds a destination to a policy with the given priority.
// Returns ErrConflict if the destination is already associated with the policy.
func (r *gormPolicyRepository) AddDestination(ctx context.Context, pd *db.PolicyDestination) error {
	if err := r.db.WithContext(ctx).Create(pd).Error; err != nil {
		return fmt.Errorf("policies: add destination: %w", err)
	}
	return nil
}

// RemoveDestination removes a destination from a policy.
// Returns ErrNotFound if the association does not exist.
func (r *gormPolicyRepository) RemoveDestination(ctx context.Context, policyID, destinationID uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("policy_id = ? AND destination_id = ?", policyID, destinationID).
		Delete(&db.PolicyDestination{})
	if result.Error != nil {
		return fmt.Errorf("policies: remove destination: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDestinationPriority updates the priority of a destination within a policy.
// Lower priority values are tried first during backup execution.
func (r *gormPolicyRepository) UpdateDestinationPriority(ctx context.Context, policyID, destinationID uuid.UUID, priority int) error {
	result := r.db.WithContext(ctx).
		Model(&db.PolicyDestination{}).
		Where("policy_id = ? AND destination_id = ?", policyID, destinationID).
		Update("priority", priority)
	if result.Error != nil {
		return fmt.Errorf("policies: update destination priority: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}