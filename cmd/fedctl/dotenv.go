package main

import (
	"bufio"
	"os"
	"strings"
)

// loadDotEnv reads a .env-format file from the current directory (if one
// exists — it's fine if it doesn't) and calls os.Setenv for every key not
// already present in the process environment. Without this, fedctl would
// only ever see .env's values through Docker Compose's env_file directive
// — i.e. inside the containers — never when fedctl itself runs as a bare
// host process (`fedctl up`, `fedctl preflight`, `fedctl check`, ...),
// which makes .env effectively decorative for half of what this tool
// does. An explicitly-exported shell variable always wins over .env, so a
// one-off override (or CI setting secrets directly) still works as
// expected.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // no .env here — most commands don't require one (e.g. `version`)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, alreadySet := os.LookupEnv(key); alreadySet {
			continue
		}
		os.Setenv(key, value)
	}
}
