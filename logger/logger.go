package logger

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
)

type MarioLogger struct {
	*logrus.Logger
}

// Log is the instance of the custom logger
var Log *MarioLogger

// ANSI escape codes for text formatting
var infoColor string
var warnColor string
var errorColor string
var debugColor string
var fatalColor string

func init() {
	// Initialize logrus logger
	logrusLogger := logrus.New()
	logrusLogger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	colorize := os.Getenv("COLORIZE_LOGS") == "true"
	if colorize {
		infoColor = "\033[1;34m%s\033[0m"
		warnColor = "\033[1;33m%s\033[0m"
		errorColor = "\033[1;31m%s\033[0m"
		debugColor = "\033[0;36m%s\033[0m"
		fatalColor = "\033[1;41m%s\033[0m"
	} else {
		infoColor = "%s"
		warnColor = "%s"
		errorColor = "%s"
		debugColor = "%s"
		fatalColor = "%s"
	}

	// Initialize custom logger
	Log = &MarioLogger{logrusLogger}
}

func (l *MarioLogger) Info(context, msg string, args ...interface{}) {
	l.Infof(infoColor, fmt.Sprintf("[LOG %s] %s", context, fmt.Sprintf(msg, args...)))
}

func (l *MarioLogger) Warn(context, msg string, args ...interface{}) {
	l.Warnf(warnColor, fmt.Sprintf("[LOG %s] %s", context, fmt.Sprintf(msg, args...)))
}

func (l *MarioLogger) Error(context, msg string, args ...interface{}) {
	l.Errorf(errorColor, fmt.Sprintf("[LOG %s] %s", context, fmt.Sprintf(msg, args...)))
}

func (l *MarioLogger) Debug(context, msg string, args ...interface{}) {
	l.Debugf(debugColor, fmt.Sprintf("[LOG %s] %s", context, fmt.Sprintf(msg, args...)))
}

func (l *MarioLogger) Fatal(context, msg string, args ...interface{}) {
	l.Fatalf(fatalColor, fmt.Sprintf("[LOG %s] %s", context, fmt.Sprintf(msg, args...)))
}
