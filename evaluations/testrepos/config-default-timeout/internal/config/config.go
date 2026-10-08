// Package config loads fetchd settings from a small "key = value" file.
//
// Every key is optional; anything the file does not mention keeps the default
// documented next to its constant below.
package config

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultAddr is the upstream used when "addr" is not set.
	DefaultAddr = "localhost:8080"
	// DefaultTimeout bounds one upstream request when "timeout" is not set.
	DefaultTimeout = 30 * time.Second
	// DefaultRetries is how many times a failed request is repeated.
	DefaultRetries = 3
)

// Config holds the settings fetchd runs with.
type Config struct {
	Addr    string
	Timeout time.Duration
	Retries int
}

// Load reads a config file. Blank lines and lines starting with "#" are
// ignored, and unknown keys are an error so typos do not go unnoticed.
func Load(r io.Reader) (Config, error) {
	cfg := Config{Addr: DefaultAddr, Retries: DefaultRetries}
	scanner := bufio.NewScanner(r)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("line %d: expected key = value", n)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "addr":
			cfg.Addr = value
		case "timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, fmt.Errorf("line %d: invalid timeout %q", n, value)
			}
			cfg.Timeout = d
		case "retries":
			v, err := strconv.Atoi(value)
			if err != nil || v < 0 {
				return Config{}, fmt.Errorf("line %d: invalid retries %q", n, value)
			}
			cfg.Retries = v
		default:
			return Config{}, fmt.Errorf("line %d: unknown key %q", n, key)
		}
	}
	return cfg, scanner.Err()
}
