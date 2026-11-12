package db

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
)

// utcConnPool hands every time to the database in UTC (R-07). SQLite stores a
// time as text with the offset of its location and compares it as text;
// pgx writes a PostgreSQL TIMESTAMP column with the wall clock, dropping the
// zone, and reads it back as UTC. Queries compare stored times with
// thresholds in UTC, so on a server outside UTC a row written now looked
// hours old. Storing UTC changes nothing shown: the GUI displays times in the
// viewer's zone, and schedules and emails use the server's.
type utcConnPool struct{ gorm.ConnPool }

func (p *utcConnPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.ConnPool.ExecContext(ctx, query, utcArgs(args)...)
}

func (p *utcConnPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.ConnPool.QueryContext(ctx, query, utcArgs(args)...)
}

func (p *utcConnPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.ConnPool.QueryRowContext(ctx, query, utcArgs(args)...)
}

// BeginTx starts a transaction whose statements get the same conversion.
func (p *utcConnPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	beginner, ok := p.ConnPool.(gorm.TxBeginner)
	if !ok {
		return nil, gorm.ErrInvalidTransaction
	}
	tx, err := beginner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	sqlDB, _ := p.GetDBConn()
	return &utcTx{utcConnPool{tx}, tx, sqlDB}, nil
}

// GetDBConn keeps gorm.DB.DB() returning the underlying *sql.DB.
func (p *utcConnPool) GetDBConn() (*sql.DB, error) {
	if sqlDB, ok := p.ConnPool.(*sql.DB); ok {
		return sqlDB, nil
	}
	return nil, gorm.ErrInvalidDB
}

type utcTx struct {
	utcConnPool
	tx *sql.Tx
	db *sql.DB // the pool the transaction came from
}

func (t *utcTx) Commit() error   { return t.tx.Commit() }
func (t *utcTx) Rollback() error { return t.tx.Rollback() }

// GetDBConn returns the pool, as gorm.DB.DB() does in a plain transaction.
func (t *utcTx) GetDBConn() (*sql.DB, error) {
	if t.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	return t.db, nil
}

// utcArgs returns args with every time in UTC, copying the slice only when
// it changes something.
func utcArgs(args []any) []any {
	var out []any
	for i, a := range args {
		var utc time.Time
		switch v := a.(type) {
		case time.Time:
			utc = v.UTC()
		case *time.Time:
			if v == nil {
				continue
			}
			utc = v.UTC()
		default:
			continue
		}
		if out == nil {
			out = append([]any(nil), args...)
		}
		out[i] = utc
	}
	if out == nil {
		return args
	}
	return out
}
