package logger

import (
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	log  *zap.SugaredLogger
	once sync.Once
)

// Init initializes the logger with the debug flag.
// If debug is true, uses a development config with debug level.
// Otherwise, uses a production config with info level.
func Init(debug bool) {
	once.Do(func() {
		var cfg zap.Config

		if debug {
			cfg = zap.NewDevelopmentConfig()
			cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
			cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		} else {
			cfg = zap.NewProductionConfig()
			cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
			cfg.EncoderConfig.TimeKey = "timestamp"
			cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		}

		logger, err := cfg.Build(zap.AddCallerSkip(1))
		if err != nil {
			panic(err)
		}

		log = logger.Sugar()

		if debug {
			log.Debug("Debug logging enabled")
		}
	})
}

// Sync flushes any buffered log entries. Should be called before exit.
func Sync() {
	if log != nil {
		_ = log.Sync()
	}
}

// Debug logs a message at debug level.
func Debug(msg string, keysAndValues ...interface{}) {
	if log != nil {
		log.Debugw(msg, keysAndValues...)
	}
}

// Info logs a message at info level.
func Info(msg string, keysAndValues ...interface{}) {
	if log != nil {
		log.Infow(msg, keysAndValues...)
	}
}

// Warn logs a message at warn level.
func Warn(msg string, keysAndValues ...interface{}) {
	if log != nil {
		log.Warnw(msg, keysAndValues...)
	}
}

// Error logs a message at error level.
func Error(msg string, keysAndValues ...interface{}) {
	if log != nil {
		log.Errorw(msg, keysAndValues...)
	}
}

// Fatal logs a message at fatal level and then calls os.Exit(1).
func Fatal(msg string, keysAndValues ...interface{}) {
	if log != nil {
		log.Fatalw(msg, keysAndValues...)
	}
}

// With returns a logger with additional context fields.
func With(keysAndValues ...interface{}) *zap.SugaredLogger {
	if log != nil {
		return log.With(keysAndValues...)
	}
	return nil
}

// L returns the underlying sugared logger for advanced use cases.
func L() *zap.SugaredLogger {
	return log
}
