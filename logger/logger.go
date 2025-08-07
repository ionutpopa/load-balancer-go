package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu       sync.Mutex
	basePath string
	file     *os.File
	logger   *log.Logger
	curDate  string
}

var defaultLogger *Logger

func InitLogger(logDir string) error {
	l := &Logger{
		basePath: logDir,
	}
	if err := l.rotate(); err != nil {
		return err
	}
	defaultLogger = l
	go l.autoRotate()
	return nil
}

func rotateFilePath(basePath string, date string) string {
	return filepath.Join(basePath, fmt.Sprintf("%s.log", date))
}

func (l *Logger) rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	date := time.Now().Format("2006-01-02")
	if date == l.curDate {
		return nil // already using today's file
	}

	// Close old file if open
	if l.file != nil {
		_ = l.file.Close()
	}

	if err := os.MkdirAll(l.basePath, 0755); err != nil {
		return err
	}

	logFilePath := rotateFilePath(l.basePath, date)
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	l.file = file
	l.curDate = date
	multi := io.MultiWriter(os.Stdout, file)
	l.logger = log.New(multi, "", log.LstdFlags|log.Lshortfile)
	return nil
}

func (l *Logger) autoRotate() {
	for {
		time.Sleep(1 * time.Minute)
		_ = l.rotate()
	}
}

// Public API
func Info(format string, v ...any) {
	if defaultLogger != nil {
		defaultLogger.output("INFO", format, v...)
	}
}

func Error(format string, v ...any) {
	if defaultLogger != nil {
		defaultLogger.output("ERROR", format, v...)
	}
}

func Debug(format string, v ...any) {
	if defaultLogger != nil {
		defaultLogger.output("DEBUG", format, v...)
	}
}

func (l *Logger) output(level string, format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix := fmt.Sprintf("[%s] ", level)
	l.logger.Output(3, prefix+fmt.Sprintf(format, v...))
}
