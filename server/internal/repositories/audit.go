package repositories

import (
	"context"
	"fmt"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"gorm.io/gorm"
)

// gormAuditRepository is the GORM implementation of AuditRepository.
type gormAuditRepository struct {
	db *gorm.DB
}

// NewAuditRepository returns an AuditRepository backed by the provided *gorm.DB.
func NewAuditRepository(db *gorm.DB) AuditRepository {
	return &gormAuditRepository{db: db}
}

// Create inserts a new audit log entry. The caller must never modify the entry
// after creation — audit records are immutable by design.
func (r *gormAuditRepository) Create(ctx context.Context, entry *db.AuditLog) error {
	if err := r.db.WithContext(ctx).Create(entry).Error; err != nil {
		return fmt.Errorf("audit: create: %w", err)
	}
	return nil
}

// List returns a paginated, filtered slice of audit log entries ordered by
// creation time descending (most recent first), together with the total count
// matching the filter (for pagination).
func (r *gormAuditRepository) List(ctx context.Context, filter AuditFilter, opts ListOptions) ([]db.AuditLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&db.AuditLog{})

	if filter.UserID != nil {
		q = q.Where("user_id = ?", filter.UserID.String())
	}
	if filter.Action != "" {
		// Support prefix matching: "policy." matches "policy.create", "policy.delete", etc.
		q = q.Where("action LIKE ?", filter.Action+"%")
	}
	if filter.ResourceType != "" {
		q = q.Where("resource_type = ?", filter.ResourceType)
	}
	if filter.From != nil {
		q = q.Where("created_at >= ?", filter.From)
	}
	if filter.To != nil {
		q = q.Where("created_at <= ?", filter.To)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("audit: list count: %w", err)
	}

	var entries []db.AuditLog
	if err := q.Order("created_at DESC").Limit(opts.Limit).Offset(opts.Offset).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}

	return entries, total, nil
}
