package logging

import (
	"log"
	"strings"
)

const (
	LevelInfo  = "info"
	LevelError = "error"
)

var currentLevel = LevelInfo

func SetLevel(level string) {
	level = strings.ToLower(level)
	if level != LevelInfo && level != LevelError {
		level = LevelInfo
	}
	currentLevel = level
	log.Printf("Log level set to %s", currentLevel)
}

func Info(msg string) {
	if currentLevel == LevelInfo {
		log.Println("[INFO] " + msg)
	}
}

func Infof(format string, args ...interface{}) {
	if currentLevel == LevelInfo {
		log.Printf("[INFO] "+format, args...)
	}
}

func Error(msg string) {
	log.Println("[ERROR] " + msg)
}

func Errorf(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}
