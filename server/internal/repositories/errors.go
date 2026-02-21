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