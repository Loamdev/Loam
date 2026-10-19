// Package config loads the optional loam.yml.
//
// It is an escape hatch, never a requirement: the zero-config path is the one
// that has to work (build spec section 12.4). A missing file is not an error,
// and every field has a sane default.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName is the optional config file Loam looks for in the project directory.
const FileName = "loam.yml"

// DefaultRows is how many rows a leaf table gets when nothing overrides it.
const DefaultRows = 10000

// Distribution maps a column value to its relative weight. Weights do not have
// to sum to 1: they are normalized at draw time, so `{active: 7, churned: 3}`
// and `{active: 0.7, churned: 0.3}` mean the same thing.
type Distribution map[string]float64

// SeedConfig tunes how much data is generated and what it looks like.
type SeedConfig struct {
	// DefaultRows is the base row count for a leaf table.
	DefaultRows int `yaml:"default_rows"`
	// Tables overrides the row count for individual tables, by table name.
	Tables map[string]int `yaml:"tables"`
	// Distributions overrides the value mix for individual columns, keyed
	// "table.column", e.g. "users.status".
	Distributions map[string]Distribution `yaml:"distributions"`
}

// Config is the parsed loam.yml. Every field is optional.
type Config struct {
	// Database overrides connection auto-detection.
	Database struct {
		// URL is the Postgres connection URL to use instead of the one derived
		// from the compose file.
		URL string `yaml:"url"`
	} `yaml:"database"`

	// App overrides the application URL Loam prints.
	App struct {
		// URL is what to print as the App URL.
		URL string `yaml:"url"`
	} `yaml:"app"`

	// Seed tunes data generation.
	Seed SeedConfig `yaml:"seed"`
}

// Default returns the configuration used when there is no loam.yml.
func Default() *Config {
	return &Config{Seed: SeedConfig{DefaultRows: DefaultRows}}
}

// Load reads dir/loam.yml.
//
// A missing file yields the defaults and a nil error: that is the happy path,
// not a failure. A file that exists but cannot be parsed IS an error, because
// silently ignoring a config the user deliberately wrote would be worse than
// stopping.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)

	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the user's own --dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()

	if err := cfg.validate(path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyDefaults fills in anything the file left unset or set to a meaningless
// value. Unmarshalling over Default() zeroes DefaultRows when the key is
// absent, so it is restored here.
func (c *Config) applyDefaults() {
	if c.Seed.DefaultRows <= 0 {
		c.Seed.DefaultRows = DefaultRows
	}
}

// validate rejects configuration that would silently produce nonsense.
func (c *Config) validate(path string) error {
	for table, rows := range c.Seed.Tables {
		if rows < 0 {
			return fmt.Errorf("%s: seed.tables.%s is %d, which is not a row count", path, table, rows)
		}
	}

	for column, dist := range c.Seed.Distributions {
		if len(dist) == 0 {
			return fmt.Errorf("%s: seed.distributions.%s lists no values", path, column)
		}
		total := 0.0
		for value, weight := range dist {
			if weight < 0 {
				return fmt.Errorf("%s: seed.distributions.%s.%s is negative", path, column, value)
			}
			total += weight
		}
		if total == 0 {
			return fmt.Errorf("%s: seed.distributions.%s has weights that are all zero", path, column)
		}
	}
	return nil
}

// RowsFor returns the configured row count for a table and whether the user
// set one explicitly.
func (c *Config) RowsFor(table string) (int, bool) {
	rows, ok := c.Seed.Tables[table]
	return rows, ok
}

// DistributionFor returns the configured value mix for "table.column" and
// whether the user set one.
func (c *Config) DistributionFor(table, column string) (Distribution, bool) {
	dist, ok := c.Seed.Distributions[table+"."+column]
	return dist, ok
}
