package logging

import (
	"log"
	"strings"
)

const (
	levelInfo  = "info"
	levelError = "error"
)

var currentLevel = levelInfo

func SetLevel(level string) {
	level = strings.ToLower(level)
	switch level {
	case levelInfo, levelError:
		currentLevel = level
	default:
		currentLevel = levelInfo
	}
	log.Printf("log level set to %s", currentLevel)
}

func Info(msg string) {
	if currentLevel == levelInfo {
		log.Println("[INFO] " + msg)
	}
}

func Infof(format string, args ...interface{}) {
	if currentLevel == levelInfo {
		log.Printf("[INFO] "+format, args...)
	}
}

func Error(msg string) {
	log.Println("[ERROR] " + msg)
}

func Errorf(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}
