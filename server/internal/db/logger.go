package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

// zapGORMLogger adapts a *zap.Logger to the gormlogger.Interface so that all
// GORM internal messages (SQL queries, slow query warnings, errors) are routed
// through the application logger instead of being written directly to stdout.
type zapGORMLogger struct {
	log                       *zap.Logger
	level                     gormlogger.LogLevel
	slowQueryThreshold        time.Duration
	ignoreRecordNotFoundError bool
}

// newZapGORMLogger returns a gormlogger.Interface backed by the provided
// *zap.Logger. Use gormlogger.Silent to disable all GORM logging, or
// gormlogger.Info to log every SQL statement (useful during development).
//
// Slow queries are logged as warnings when they exceed 200ms. To disable slow
// query detection set SlowQueryThreshold to 0 in the returned struct.
func newZapGORMLogger(log *zap.Logger, level gormlogger.LogLevel) gormlogger.Interface {
	if level == 0 {
		level = gormlogger.Warn
	}
	return &zapGORMLogger{
		log:                       log.WithOptions(zap.AddCallerSkip(3)),
		level:                     level,
		slowQueryThreshold:        200 * time.Millisecond,
		ignoreRecordNotFoundError: true,
	}
}

// LogMode returns a new logger instance with the given log level.
// GORM calls this internally when it needs to override the log level for a
// specific operation (e.g. db.Debug() sets level to Info for that call).
func (l *zapGORMLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	copy := *l
	copy.level = level
	return &copy
}

// Info logs informational messages emitted by GORM internals.
func (l *zapGORMLogger) Info(_ context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Info {
		l.log.Info(fmt.Sprintf(msg, args...))
	}
}

// Warn logs warning messages emitted by GORM internals.
func (l *zapGORMLogger) Warn(_ context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Warn {
		l.log.Warn(fmt.Sprintf(msg, args...))
	}
}

// Error logs error messages emitted by GORM internals.
func (l *zapGORMLogger) Error(_ context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Error {
		l.log.Error(fmt.Sprintf(msg, args...))
	}
}

// Trace logs individual SQL statements along with their execution time and
// the number of rows affected. It also emits a warning for slow queries.
//
// gorm.ErrRecordNotFound is silenced by default because it is a normal
// application-level condition, not a database error.
func (l *zapGORMLogger) Trace(_ context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	fields := []zap.Field{
		zap.String("sql", sql),
		zap.Duration("elapsed", elapsed),
		zap.Int64("rows", rows),
		zap.String("caller", utils.FileWithLineNum()),
	}

	switch {
	case err != nil && !(l.ignoreRecordNotFoundError && errors.Is(err, gorm.ErrRecordNotFound)):
		// Log actual database errors at error level.
		l.log.Error("gorm query error", append(fields, zap.Error(err))...)

	case l.slowQueryThreshold > 0 && elapsed > l.slowQueryThreshold:
		// Log slow queries at warn level so they are visible without enabling
		// full SQL tracing (gormlogger.Info).
		l.log.Warn("gorm slow query", fields...)

	case l.level >= gormlogger.Info:
		// Full SQL tracing — only active when log level is Info or higher.
		l.log.Debug("gorm query", fields...)
	}
}