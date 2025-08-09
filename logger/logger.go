package logger

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// MaxLogSize is the maximum size of a log file before rotation (100MB)
	MaxLogSize = 100 * 1024 * 1024 // 100MB in bytes

	// CheckInterval is how often we check for rotation needs
	CheckInterval = 30 * time.Second
)

type Logger struct {
	mu       sync.Mutex
	basePath string
	file     *os.File
	logger   *log.Logger
	curDate  string
	fileSize int64 // Track current file size
}

var defaultLogger *Logger

// InitLogger initializes the global logger with the specified directory
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

// rotateFilePath generates the path for a log file
func rotateFilePath(basePath string, date string) string {
	return filepath.Join(basePath, fmt.Sprintf("%s.log", date))
}

// archivedFilePath generates the path for an archived (compressed) log file
func archivedFilePath(basePath string, date string, timestamp string) string {
	return filepath.Join(basePath, fmt.Sprintf("%s_%s.log.gz", date, timestamp))
}

// rotate creates a new log file and optionally compresses the old one if it's too large
func (l *Logger) rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	date := time.Now().Format("2006-01-02")

	// Check if we need to rotate due to date change or file size
	needsRotation := false

	if date != l.curDate {
		needsRotation = true
	} else if l.file != nil {
		// Check current file size
		if stat, err := l.file.Stat(); err == nil {
			l.fileSize = stat.Size()
			if l.fileSize >= MaxLogSize {
				needsRotation = true
			}
		}
	}

	if !needsRotation {
		return nil
	}

	// If we have an existing file that needs to be archived
	if l.file != nil {
		oldFilePath := l.file.Name()

		// Close the current file
		if err := l.file.Close(); err != nil {
			// Log to stderr since our logger might not be working
			fmt.Fprintf(os.Stderr, "Error closing log file: %v\n", err)
		}

		// Check if the old file should be compressed (if it's large enough)
		if stat, err := os.Stat(oldFilePath); err == nil && stat.Size() > 1024*1024 { // Only compress files larger than 1MB
			if err := l.compressLogFile(oldFilePath); err != nil {
				fmt.Fprintf(os.Stderr, "Error compressing log file %s: %v\n", oldFilePath, err)
			}
		}

		l.file = nil
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(l.basePath, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Determine the new log file path
	var logFilePath string
	if date != l.curDate {
		// Date-based rotation
		logFilePath = rotateFilePath(l.basePath, date)
	} else {
		// Size-based rotation - add timestamp to make it unique
		timestamp := time.Now().Format("15-04-05") // HH-MM-SS
		logFilePath = filepath.Join(l.basePath, fmt.Sprintf("%s_%s.log", date, timestamp))
	}

	// Open new log file
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logFilePath, err)
	}

	// Set up the logger
	l.file = file
	l.curDate = date
	l.fileSize = 0 // Reset size counter for new file

	// Get initial file size if file already exists
	if stat, err := file.Stat(); err == nil {
		l.fileSize = stat.Size()
	}

	multi := io.MultiWriter(os.Stdout, file)
	l.logger = log.New(multi, "", log.LstdFlags|log.Lshortfile)

	// Log the rotation event
	l.logRotationEvent(logFilePath)

	return nil
}

// compressLogFile compresses a log file and removes the original
func (l *Logger) compressLogFile(filePath string) error {
	// Generate compressed file name with timestamp
	dir := filepath.Dir(filePath)
	baseName := filepath.Base(filePath)
	timestamp := time.Now().Format("15-04-05") // HH-MM-SS format

	// Remove .log extension if present and add timestamp
	if filepath.Ext(baseName) == ".log" {
		baseName = baseName[:len(baseName)-4] // Remove .log
	}
	compressedPath := filepath.Join(dir, fmt.Sprintf("%s_%s.log.gz", baseName, timestamp))

	// Open source file
	srcFile, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	// Create compressed file
	dstFile, err := os.Create(compressedPath)
	if err != nil {
		return fmt.Errorf("failed to create compressed file: %w", err)
	}
	defer dstFile.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(dstFile)
	defer gzWriter.Close()

	// Set gzip header info
	gzWriter.Name = filepath.Base(filePath)
	gzWriter.ModTime = time.Now()

	// Copy and compress
	if _, err := io.Copy(gzWriter, srcFile); err != nil {
		// Clean up partial compressed file
		os.Remove(compressedPath)
		return fmt.Errorf("failed to compress file: %w", err)
	}

	// Close gzip writer to ensure all data is written
	if err := gzWriter.Close(); err != nil {
		os.Remove(compressedPath)
		return fmt.Errorf("failed to finalize compressed file: %w", err)
	}

	// Close destination file
	if err := dstFile.Close(); err != nil {
		return fmt.Errorf("failed to close compressed file: %w", err)
	}

	// Remove original file only after successful compression
	if err := os.Remove(filePath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove original log file %s: %v\n", filePath, err)
		// Don't return error here as compression was successful
	}

	return nil
}

// logRotationEvent logs information about the rotation
func (l *Logger) logRotationEvent(newFilePath string) {
	msg := fmt.Sprintf("Log rotated to: %s", newFilePath)
	if l.logger != nil {
		l.logger.Output(2, fmt.Sprintf("[INFO] %s", msg))
	} else {
		// Fallback to stderr if logger isn't ready
		fmt.Fprintf(os.Stderr, "%s [INFO] %s\n", time.Now().Format("2006/01/02 15:04:05"), msg)
	}
}

// autoRotate runs in a goroutine to periodically check for rotation needs
func (l *Logger) autoRotate() {
	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := l.rotate(); err != nil {
			fmt.Fprintf(os.Stderr, "Auto-rotation error: %v\n", err)
		}
	}
}

// updateFileSize should be called after writing to update the file size counter
func (l *Logger) updateFileSize(bytesWritten int) {
	l.fileSize += int64(bytesWritten)
}

// output writes a log message and updates the file size counter
func (l *Logger) output(level string, format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.logger == nil {
		return
	}

	message := fmt.Sprintf("[%s] %s", level, fmt.Sprintf(format, v...))

	// Calculate approximate message size (this is an approximation)
	// In practice, the actual written size might be slightly different due to timestamps
	messageSize := len(message) + 25 // +25 for timestamp and formatting

	l.logger.Output(3, message)
	l.updateFileSize(messageSize)

	// Check if we need to rotate due to size (non-blocking check)
	if l.fileSize >= MaxLogSize {
		go func() {
			if err := l.rotate(); err != nil {
				fmt.Fprintf(os.Stderr, "Size-based rotation error: %v\n", err)
			}
		}()
	}
}

// GetCurrentLogInfo returns information about the current log file
func (l *Logger) GetCurrentLogInfo() (string, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return "", 0, fmt.Errorf("no active log file")
	}

	// Get fresh file size from filesystem
	stat, err := l.file.Stat()
	if err != nil {
		return l.file.Name(), l.fileSize, err
	}

	l.fileSize = stat.Size() // Update our counter
	return l.file.Name(), l.fileSize, nil
}

// Public API functions
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

// GetLogInfo returns information about the current log file
func GetLogInfo() (string, int64, error) {
	if defaultLogger != nil {
		return defaultLogger.GetCurrentLogInfo()
	}
	return "", 0, fmt.Errorf("logger not initialized")
}

// ForceRotation forces a log rotation (useful for testing or manual rotation)
func ForceRotation() error {
	if defaultLogger != nil {
		return defaultLogger.rotate()
	}
	return fmt.Errorf("logger not initialized")
}
