// Command snapshotpruner deletes the snapshot images of a GitHub Container Registry package older than a number of days, with the platform images only they use, leaving every release's image.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv("GITHUB_TOKEN"), os.Stdout, http.DefaultClient,
		"https://api.github.com", "https://ghcr.io", time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "snapshotpruner:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, token string, output io.Writer, client *http.Client,
	apiURL, registryURL string, now time.Time) error {
	flags := flag.NewFlagSet("snapshotpruner", flag.ContinueOnError)
	flags.SetOutput(output)
	owner := flags.String("owner", "", "the organization owning the package")
	packageName := flags.String("package", "", "the container package")
	maxAgeDays := flags.Int("max-age-days", 0, "how many days a snapshot image is kept")
	dryRun := flags.Bool("dry-run", false, "name the images it would delete, deleting none")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	switch {
	case *owner == "" || *packageName == "":
		return errors.New("--owner and --package are required")
	case *maxAgeDays < 1:
		return errors.New("--max-age-days is required, at least 1")
	case token == "":
		return errors.New("GITHUB_TOKEN is required")
	}
	registry := githubContainerRegistry{
		client: client, apiURL: apiURL, registryURL: registryURL, owner: *owner, packageName: *packageName, token: token,
	}
	deadline := now.Add(-time.Duration(*maxAgeDays) * 24 * time.Hour)
	deleted, err := pruneSnapshots(ctx, registry, deadline, *dryRun)
	verb := "deleted"
	if *dryRun {
		verb = "would delete"
	}
	for _, digest := range deleted {
		fmt.Fprintln(output, verb, digest)
	}
	return err
}
