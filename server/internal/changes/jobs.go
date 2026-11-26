// Package changes tells connected GUI clients that objects changed, so the
// pages showing them refresh themselves instead of polling. Repositories are
// wrapped in decorators that publish a websocket.MsgChange on the topic of
// its type (websocket.ChangeTopic) after every successful write: every write goes
// through the repository, whatever package makes it, so none can forget to
// publish. The transactions are inside the repository methods, so a client
// that refreshes on the event reads the committed state.
package changes

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// Publisher sends a message to the clients subscribed to a topic.
// Satisfied by *websocket.Hub, whose Publish never blocks: a slow client
// loses the message instead of holding up the write.
type Publisher interface {
	Publish(topic string, msg websocket.Message)
}

// jobRepository publishes a "jobs" change after each successful write of
// the JobRepository it wraps. Reads go straight to the wrapped repository.
// Job logs are not announced: they do not change the job lists, and the job
// page receives them as job.log messages on job:<id>.
type jobRepository struct {
	repositories.JobRepository
	notifier
}

// NewJobRepository wraps jobs so that its writes publish a "jobs" change on
// pub.
func NewJobRepository(jobs repositories.JobRepository, pub Publisher) repositories.JobRepository {
	return &jobRepository{JobRepository: jobs, notifier: newNotifier(pub, websocket.ChangeTypeJobs)}
}

func (r *jobRepository) Create(ctx context.Context, job *db.Job) error {
	err := r.JobRepository.Create(ctx, job)
	return r.after(job.ID, err)
}

func (r *jobRepository) Update(ctx context.Context, job *db.Job) error {
	return r.after(job.ID, r.JobRepository.Update(ctx, job))
}

func (r *jobRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, startedAt *time.Time, endedAt *time.Time, errMsg string) error {
	return r.after(id, r.JobRepository.UpdateStatus(ctx, id, status, startedAt, endedAt, errMsg))
}

func (r *jobRepository) MarkRunningJobsInterruptedForAgent(ctx context.Context, agentID uuid.UUID, errMsg string) (int64, error) {
	return r.afterBulk(r.JobRepository.MarkRunningJobsInterruptedForAgent(ctx, agentID, errMsg))
}

func (r *jobRepository) MarkRunningJobsInterrupted(ctx context.Context, errMsg string) (int64, error) {
	return r.afterBulk(r.JobRepository.MarkRunningJobsInterrupted(ctx, errMsg))
}

func (r *jobRepository) MarkResumeExhausted(ctx context.Context, id uuid.UUID, errMsg string) error {
	return r.after(id, r.JobRepository.MarkResumeExhausted(ctx, id, errMsg))
}

func (r *jobRepository) CreateDestination(ctx context.Context, jd *db.JobDestination) error {
	err := r.JobRepository.CreateDestination(ctx, jd)
	return r.after(jd.JobID, err)
}

func (r *jobRepository) UpdateDestinationStatus(ctx context.Context, jobID uuid.UUID, destID uuid.UUID, status string, startedAt *time.Time, endedAt *time.Time, snapshotID string, sizeBytes int64, errMsg string) error {
	return r.after(jobID, r.JobRepository.UpdateDestinationStatus(ctx, jobID, destID, status, startedAt, endedAt, snapshotID, sizeBytes, errMsg))
}

func (r *jobRepository) UpsertDestinationCommandResult(ctx context.Context, jobID, destID uuid.UUID, sourceName, status string, startedAt, endedAt *time.Time, snapshotID string, sizeBytes int64, errMsg string) error {
	return r.after(jobID, r.JobRepository.UpsertDestinationCommandResult(ctx, jobID, destID, sourceName, status, startedAt, endedAt, snapshotID, sizeBytes, errMsg))
}

func (r *jobRepository) CreateRetentionTag(ctx context.Context, t *db.JobRetentionTag) error {
	err := r.JobRepository.CreateRetentionTag(ctx, t)
	return r.after(t.JobID, err)
}

func (r *jobRepository) UpdateRetentionTagStatus(ctx context.Context, jobID, destID uuid.UUID, tag, status string, startedAt, endedAt *time.Time, errMsg string) error {
	return r.after(jobID, r.JobRepository.UpdateRetentionTagStatus(ctx, jobID, destID, tag, status, startedAt, endedAt, errMsg))
}
