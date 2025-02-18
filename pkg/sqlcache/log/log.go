package log

import (
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

func shouldLog(name string) bool {
	storesToLog := strings.Split(os.Getenv("DEBUG_SQLCACHE"), ",")
	if len(storesToLog) == 0 {
		return false
	}
	for _, toLogName := range storesToLog {
		if toLogName == name {
			return true
		}
	}
	return false
}

func Debugln(name string, a ...any) {
	if !shouldLog(name) {
		return
	}
	logrus.Info(append([]any{"[" + name + "] HITHERE:"}, a...)...)
}

func Debugf(name, format string, a ...any) {
	if !shouldLog(name) {
		return
	}
	logrus.Infof("["+name+"] HITHERE: "+format, a...)
}
