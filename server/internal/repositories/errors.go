package repositories

import "errors"

// ErrNotFound is returned by repository methods when the requested record
// does not exist in the database. Callers should check for this error
// explicitly using errors.Is to distinguish missing records from other
// database errors.
//
//	user, err := repo.GetByID(ctx, id)
//	if errors.Is(err, repositories.ErrNotFound) {
//	    handle not found
//	}
var ErrNotFound = errors.New("record not found")

// ErrConflict is returned when an insert or update violates a unique constraint,
// for example when registering a user with an email that already exists.
var ErrConflict = errors.New("record already exists")

// ErrTerminalState is returned by JobRepository.UpdateStatus when the job has
// already reached a terminal status and the update was therefore refused.
//
// It is expected rather than exceptional: an agent whose connection dropped
// mid-backup keeps running restic and reports the outcome after reconnecting,
// by which time the server has already recorded the job as interrupted. Without
// the refusal that late report would resurrect a finished job.
var ErrTerminalState = errors.New("job already in a terminal state")

// ErrDestinationBusy is returned by DestinationRepository.ClaimForRepair when
// a job still holds the destination: repairs are refused, not queued
// (ADR 0026).
var ErrDestinationBusy = errors.New("the destination is busy with another job")

// ErrRepairInProgress is returned when a repair of the destination is pending
// or running: by ClaimForRepair, since repairs never overlap, and by
// EndMaintenance, since ending the maintenance would let other jobs start
// against the repository being repaired.
var ErrRepairInProgress = errors.New("a repair of this destination is in progress")
