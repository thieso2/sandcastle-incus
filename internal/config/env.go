package config

import (
	"os"
	"strings"
)

func loadAdminEnv() map[string]string {
	values := map[string]string{}
	for key, value := range readDotEnv(".env") {
		values[key] = value
	}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		values[key] = value
	}
	return values
}

func readDotEnv(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := parseDotEnvLine(line)
		if ok {
			values[key] = value
		}
	}
	return values
}

func parseDotEnvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			value = value[1 : len(value)-1]
		}
	} else if before, _, ok := strings.Cut(value, "#"); ok {
		value = strings.TrimSpace(before)
	}
	return key, value, true
}

func getenvFrom(values map[string]string, key string, fallback string) string {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return fallback
	}
	return value
}

func splitListFrom(values map[string]string, key string) []string {
	return splitList(values[key])
}
