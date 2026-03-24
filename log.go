package main

import (
	"fmt"
	"os"
)

var (
	bold   string
	dim    string
	red    string
	green  string
	yellow string
	cyan   string
	reset  string
)

func init() {
	if isTerminal(os.Stderr) {
		bold = "\033[1m"
		dim = "\033[2m"
		red = "\033[31m"
		green = "\033[32m"
		yellow = "\033[33m"
		cyan = "\033[36m"
		reset = "\033[0m"
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func logMsg(msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s%s==>%s %s\n", cyan, bold, reset, s)
}

func logInfo(msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "    %s%s%s\n", dim, s, reset)
}

func logErr(msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s%sError:%s %s\n", red, bold, reset, s)
}

func logOk(msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s%s==>%s %s\n", green, bold, reset, s)
}

func logWarn(msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s%s==>%s %s\n", yellow, bold, reset, s)
}

func fmtDuration(seconds int) string {
	if seconds >= 3600 {
		return fmt.Sprintf("%dh%02dm%02ds", seconds/3600, (seconds%3600)/60, seconds%60)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}
