package logger

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
)

type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var currentLevel atomic.Int32

func init() { currentLevel.Store(int32(LevelInfo)) }

func SetLevel(l Level)    { currentLevel.Store(int32(l)) }
func CurrentLevel() Level { return Level(currentLevel.Load()) }

func LevelFromString(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

type secret struct{ plain, label string }

var (
	secretsMu sync.RWMutex
	secrets   []secret
)

func RegisterSecret(value, label string) {
	if value == "" {
		return
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	for i, s := range secrets {
		if s.label == label {
			secrets[i].plain = value
			return
		}
	}
	secrets = append(secrets, secret{plain: value, label: label})
}

func redact(s string) string {
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	for _, sec := range secrets {
		s = strings.ReplaceAll(s, sec.plain, sec.label)
	}
	return s
}

func write(l Level, format string, args ...any) {
	if Level(currentLevel.Load()) > l {
		return
	}
	tag := map[Level]string{
		LevelDebug: "[DEBUG] ",
		LevelInfo:  "[INFO]  ",
		LevelWarn:  "[WARN]  ",
		LevelError: "[ERROR] ",
	}[l]
	log.Print(tag + redact(fmt.Sprintf(format, args...)))
}

func Debug(format string, args ...any) { write(LevelDebug, format, args...) }
func Info(format string, args ...any)  { write(LevelInfo, format, args...) }
func Warn(format string, args ...any)  { write(LevelWarn, format, args...) }
func Error(format string, args ...any) { write(LevelError, format, args...) }
