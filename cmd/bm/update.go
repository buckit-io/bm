package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/buckit-io/bm/internal/update"
)

func runUpdate(rawArgs []string) error {
	fs := flag.NewFlagSet("bm update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "check for a newer release without applying it")
	if err := fs.Parse(rawArgs); err != nil {
		return err
	}

	svc := update.NewService()
	ctx := context.Background()

	if *checkOnly {
		status, err := svc.Check(ctx)
		if err != nil {
			return err
		}
		printUpdateStatus(status)
		return nil
	}

	result, err := svc.Apply(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, result.Message)
	return nil
}

func printUpdateStatus(status update.Status) {
	fmt.Fprintf(os.Stdout, "Running version:   %s\n", status.CurrentVersion)
	fmt.Fprintf(os.Stdout, "Installed on disk: %s\n", status.InstalledVersion)
	if status.LatestVersion != "" {
		fmt.Fprintf(os.Stdout, "Latest release:    %s\n", status.LatestVersion)
	}
	switch {
	case status.UpdateAvailable && status.CanApply:
		fmt.Fprintln(os.Stdout, "Update available.")
	case status.UpdateAvailable:
		fmt.Fprintf(os.Stdout, "Update available, but cannot be applied automatically: %s\n", status.Reason)
	case status.RestartRequired:
		fmt.Fprintln(os.Stdout, status.Reason)
	default:
		fmt.Fprintln(os.Stdout, "Already up to date.")
	}
}
