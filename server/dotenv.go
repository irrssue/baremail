package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv reads a .env file (if present) and sets any keys not already in the
// environment — a minimal stand-in for Node's dotenv. Looks in the working dir
// and one level up (so it finds server/.env whether run from server-go or root).
// Real env vars always win; existing values are never overwritten.
func loadDotEnv() {
	for _, p := range candidateEnvPaths() {
		if applyEnvFile(p) {
			return
		}
	}
}

func candidateEnvPaths() []string {
	wd, _ := os.Getwd()
	return []string{
		filepath.Join(wd, ".env"),
		filepath.Join(wd, "..", ".env"),
		filepath.Join(wd, "..", "server", ".env"),
	}
}

func applyEnvFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"'`) // strip surrounding quotes
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return true
}
