// Command loam is a local-only CLI that boots an isolated Docker Compose
// stack, seeds its Postgres database with foreign-key-correct synthetic data,
// and tears it all down again.
//
// This file is deliberately thin (build spec section 4): it wires nothing and
// decides nothing. All command construction lives in internal/cli.
package main

import (
	"os"

	"github.com/loamdev/loam/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
