package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

func setErrlog(cmd string) *log.Logger {
	return log.New(os.Stderr, fmt.Sprintf("%s %s: ", appName, cmd), log.Ldate|log.Ltime)
}

// splitAndClean removes repeated componens of a comma separated string
// and returns unique strings
func splitAndClean(s string) []string {
	components := strings.Split(s, ",")
	m := make(map[string]bool)
	for _, c := range components {
		m[c] = true
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
