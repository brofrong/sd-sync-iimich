package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	mu     sync.RWMutex
	logger *slog.Logger
	logFile *os.File
)

func LogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "sd-sync-immich", "app.log")
}

func LogDir() string {
	return filepath.Dir(LogPath())
}

// Init configures file logging. Returns the log file path.
func Init() (string, error) {
	path := LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}

	handler := slog.NewTextHandler(f, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})

	mu.Lock()
	logFile = f
	logger = slog.New(handler)
	slog.SetDefault(logger)
	mu.Unlock()

	Info("logger initialized", "path", path)
	return path, nil
}

func Writer() io.Writer {
	mu.RLock()
	defer mu.RUnlock()
	if logFile != nil {
		return logFile
	}
	return os.Stderr
}

func Info(msg string, args ...any)  { log(slog.LevelInfo, msg, args...) }
func Warn(msg string, args ...any)  { log(slog.LevelWarn, msg, args...) }
func Error(msg string, args ...any) { log(slog.LevelError, msg, args...) }
func Debug(msg string, args ...any) { log(slog.LevelDebug, msg, args...) }

func log(level slog.Level, msg string, args ...any) {
	mu.RLock()
	l := logger
	mu.RUnlock()
	if l != nil {
		l.Log(nil, level, msg, args...)
		return
	}
	slog.Log(nil, level, msg, args...)
}

// OpenLog opens the log file in the default macOS text editor.
func OpenLog() error {
	path := LogPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("log file not found: %s", path)
	}
	return exec.Command("open", path).Run()
}

func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		err := logFile.Close()
		logFile = nil
		return err
	}
	return nil
}
