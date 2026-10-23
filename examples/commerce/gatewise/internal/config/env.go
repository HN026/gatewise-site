package config

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// readEnvFile supports KEY=VALUE, optional export, quotes, and comments.
// Values are not shell-executed or recursively interpolated.
func readEnvFile(filename string) (map[string]string, error) {
	values := map[string]string{}
	if filename == "" {
		return values, nil
	}
	f, err := os.Open(filename)
	if os.IsNotExist(err) && filename == ".env" {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	defer f.Close()
	keys := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		fail := func() error { return fmt.Errorf("env file line %d: expected KEY=VALUE with valid quoting", line) }
		if !ok || !keys.MatchString(key) {
			return nil, fail()
		}
		if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
			quote := value[0]
			end := -1
			for i := 1; i < len(value); i++ {
				if quote == '"' && value[i] == '\\' {
					i++
					continue
				}
				if value[i] == quote {
					end = i
					break
				}
			}
			if end < 0 {
				return nil, fail()
			}
			tail := strings.TrimSpace(value[end+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return nil, fail()
			}
			quoted := value[:end+1]
			if quote == '"' {
				value, err = strconv.Unquote(quoted)
				if err != nil {
					return nil, fail()
				}
			} else {
				value = quoted[1 : len(quoted)-1]
			}
		} else {
			for i := 0; i < len(value); i++ {
				if value[i] == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
					value = strings.TrimSpace(value[:i])
					break
				}
			}
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return values, nil
}
