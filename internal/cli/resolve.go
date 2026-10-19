package cli

import (
	"fmt"

	"github.com/loamdev/loam/internal/config"
	"github.com/loamdev/loam/internal/env"
)

// resolveDBURL works out which Postgres to talk to.
//
// Precedence, highest first:
//
//  1. --db-url, because a flag the user typed on this run must win;
//  2. database.url in loam.yml, the escape hatch for repos auto-detection
//     cannot read;
//  3. the compose file itself, which is the zero-config path and the one that
//     has to work without any of the above (build spec section 12.4).
//
// The returned source is a short human-readable label for error messages.
func resolveDBURL(cfg *config.Config, compose *env.ComposeFile) (dbURL, source string, err error) {
	if opts.dbURL != "" {
		return opts.dbURL, "--db-url", nil
	}
	if cfg != nil && cfg.Database.URL != "" {
		return cfg.Database.URL, config.FileName, nil
	}

	url, err := compose.PostgresURL()
	if err != nil {
		return "", "", fmt.Errorf("%w; set database.url in %s or pass --db-url", err, config.FileName)
	}
	return url, compose.Path, nil
}

// resolveAppURL picks the URL to print as the app's front door: the config
// override when there is one, otherwise a best-effort guess from the compose
// file. An empty result means "do not print an App line".
func resolveAppURL(cfg *config.Config, compose *env.ComposeFile) string {
	if cfg != nil && cfg.App.URL != "" {
		return cfg.App.URL
	}
	return compose.AppURL()
}
