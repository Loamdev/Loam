package main

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "playbrige",
		Short: "Playbrige CLI",
	}

	envCmd := &cobra.Command{Use: "env", Short: "Manage environments"}
	envCmd.AddCommand(
		&cobra.Command{
			Use:   "create",
			Short: "Create environment",
			Run: func(_ *cobra.Command, _ []string) {
				fmt.Printf("environment creation wiring pending, request id: %s\n", uuid.NewString())
			},
		},
		&cobra.Command{
			Use:   "destroy [id]",
			Short: "Destroy environment",
			Args:  cobra.ExactArgs(1),
			Run: func(_ *cobra.Command, args []string) {
				fmt.Printf("environment destruction wiring pending for %s\n", args[0])
			},
		},
		&cobra.Command{
			Use:   "status [id]",
			Short: "Environment status",
			Args:  cobra.ExactArgs(1),
			Run: func(_ *cobra.Command, args []string) {
				fmt.Printf("status wiring pending for %s\n", args[0])
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List environments",
			Run: func(_ *cobra.Command, _ []string) {
				fmt.Println("environment list wiring pending")
			},
		},
	)

	rootCmd.AddCommand(envCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
