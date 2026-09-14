package logger

import (
	"io"
	"log/slog"
	"os"

	"github.com/getoptimum/optimum-common/pkg/version"
)

// LogMode defines logging verbosity levels.
type LogMode string

const (
	Debug      LogMode = "debug"
	Info       LogMode = "info"
	Production LogMode = "production"
	Warning    LogMode = "warning"
	Error      LogMode = "error"
)

func validateLogMode(mode LogMode) LogMode {
	switch mode {
	case Info, Debug, Warning, Error:
		return mode
	default:
		return Info
	}
}

// Field represents a typed key-value pair for structured logging.
type Field struct {
	a slog.Attr
}

// AppLogger defines the structured logger interface.
type AppLogger interface {
	Info(message string, fields ...Field)
	Error(message string, err error, fields ...Field)
	Debug(message string, fields ...Field)
	Fatal(message string, err error, fields ...Field)
	With(fields ...Field) AppLogger
	Slog() *slog.Logger
}

// SLogger is an implementation of AppLogger backed by slog.
type SLogger struct {
	logger *slog.Logger
}

var _ AppLogger = (*SLogger)(nil)

// NewAppSLogger creates a default AppLogger writing to STDERR.
func NewAppSLogger(mode LogMode, fields ...Field) AppLogger {
	return InitLogger([]io.Writer{os.Stderr}, validateLogMode(mode), fields...)
}

// NewAppLogger creates a default AppLogger writing to STDERR.
func NewAppLogger(mode LogMode, fields ...Field) *SLogger {
	al := InitLogger([]io.Writer{os.Stderr}, validateLogMode(mode), fields...)
	if sl, ok := al.(*SLogger); ok {
		return sl
	}
	al.Fatal("InitLogger did not return *SLogger", nil)
	return nil
}

// InitLogger initializes a multi-output JSON logger with slog. At least one writer must be provided.
func InitLogger(writers []io.Writer, mode LogMode, fields ...Field) AppLogger {
	handlers := make([]slog.Handler, len(writers))
	for i, w := range writers {
		handlers[i] = newDedupHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: logLevelFromMode(mode),
			// ReplaceAttr runs for every attribute, including the ones callers pass, so each
			// branch checks the value kind: a user field named "time" carries a string, not a
			// time, and reading it as one panics inside the handler.
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if len(groups) > 0 {
					return a
				}

				switch a.Key {
				case "time":
					if a.Value.Kind() == slog.KindTime {
						return slog.Int64("timestamp", a.Value.Time().Unix())
					}

				case "gray_log_level":
					if a.Value.Kind() == slog.KindInt64 {
						return slog.Int64("level", a.Value.Int64())
					}
				}

				return a
			},
		}))
	}

	attrs := make([]any, 0, len(fields)+2)
	for _, f := range fields {
		attrs = append(attrs, f.a)
	}

	attrs = append(attrs,
		slog.String("commit", version.GetCommitHash()),
		slog.String("version", version.GetVersion()),
	)

	mh := slog.NewMultiHandler(handlers...)
	return &SLogger{logger: slog.New(mh).With(attrs...)}
}

// Info logs an informational message with optional structured fields.
func (l *SLogger) Info(message string, fields ...Field) {
	params := prepareSlogParams(nil, fields)
	l.logger.Info(message, params...)
}

// Error logs an error message with associated error and fields.
func (l *SLogger) Error(message string, err error, fields ...Field) {
	params := prepareSlogParams(err, fields)
	l.logger.Error(message, params...)
}

// Debug logs a debug message.
func (l *SLogger) Debug(message string, fields ...Field) {
	params := prepareSlogParams(nil, fields)
	l.logger.Debug(message, params...)
}

// Fatal logs an error and exits the application.
func (l *SLogger) Fatal(message string, err error, fields ...Field) {
	params := prepareSlogParams(err, fields)
	l.logger.Error(message, params...)
	l.logger.Info("fatal error; exiting")
	os.Exit(1)
}

// With creates a child logger with additional structured context.
func (l *SLogger) With(fields ...Field) AppLogger {
	return &SLogger{logger: l.logger.With(prepareSlogParams(nil, fields)...)}
}

// Slog returns the underlying *slog.Logger.
func (l *SLogger) Slog() *slog.Logger {
	return l.logger
}

func prepareSlogParams(err error, fields []Field) []any {
	params := make([]any, 0, len(fields)+2)
	if err != nil {
		params = append(params, slog.String("error", err.Error()))
	}

	for _, f := range fields {
		params = append(params, f.a)
	}

	return params
}

func logLevelFromMode(mode LogMode) slog.Level {
	mapper := map[LogMode]slog.Level{
		Debug:      slog.LevelDebug,
		Info:       slog.LevelInfo,
		Production: slog.LevelInfo,
		Warning:    slog.LevelWarn,
		Error:      slog.LevelError,
	}
	if m, ok := mapper[mode]; ok {
		return m
	}
	return slog.LevelInfo
}
