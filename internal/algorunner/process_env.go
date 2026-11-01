package algorunner

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// LoadDotEnv reads key-value environment variable pairs from a .env file.
//
// A missing file is not an error (the file is optional); anything else is
// returned as-is so the caller can report a real parse failure.
func LoadDotEnv(filePath string) (map[string]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	defer file.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return env, scanner.Err()
}
