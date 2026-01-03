package logging

import (
	"io"
	"log"
	"os"
	"strings"
)

const (
	levelInfo  = "info"
	levelError = "error"
)

var currentLevel = levelInfo

var (
	infoLogger  = log.New(os.Stdout, "", log.LstdFlags)
	errorLogger = log.New(os.Stderr, "", log.LstdFlags)
)

func SetLevel(level string) {
	level = strings.ToLower(level)
	switch level {
	case levelInfo, levelError:
		currentLevel = level
	default:
		currentLevel = levelInfo
	}
	infoLogger.Printf("log level set to %s", currentLevel)
}

func SetOutputs(infoOut, errOut io.Writer) {
	if infoOut != nil {
		infoLogger.SetOutput(infoOut)
	}
	if errOut != nil {
		errorLogger.SetOutput(errOut)
	}
}

func Info(msg string) {
	if currentLevel == levelInfo {
		infoLogger.Println("[INFO] " + msg)
	}
}

func Infof(format string, args ...interface{}) {
	if currentLevel == levelInfo {
		infoLogger.Printf("[INFO] "+format, args...)
	}
}

func Error(msg string) {
	errorLogger.Println("[ERROR] " + msg)
}

func Errorf(format string, args ...interface{}) {
	errorLogger.Printf("[ERROR] "+format, args...)
}
