// Command apispec downloads Discord's OpenAPI spec and reports spec changes
// for the coverage watcher workflow.
//
//	apispec fetch -o FILE            download the pinned spec and verify its checksum
//	apispec fetch -latest -o FILE    download the latest spec; print its commit and checksum
//	apispec watch -pinned FILE -latest FILE -commit SHA [-manifest FILE]
//	                                 print the changes that need issues as JSON
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/smoketurner/terraform-provider-discord/internal/apispec"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "apispec:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: apispec fetch|watch [flags]")
	}
	switch args[0] {
	case "fetch":
		return fetch(ctx, args[1:], stdout)
	case "watch":
		return watch(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func fetch(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	out := fs.String("o", "", "write the spec to this file")
	latest := fs.Bool("latest", false, "download the head of main instead of the pinned commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("fetch: -o is required")
	}

	c := &http.Client{Timeout: 2 * time.Minute}
	commit := apispec.PinnedCommit
	if *latest {
		var err error
		if commit, err = apispec.LatestCommit(ctx, c, apispec.APIBaseURL); err != nil {
			return err
		}
	}
	b, err := apispec.FetchSpec(ctx, c, apispec.RawBaseURL, commit)
	if err != nil {
		return err
	}
	if !*latest {
		if err := apispec.VerifySHA256(b, apispec.PinnedSHA256); err != nil {
			return err
		}
	}
	if _, err := apispec.ParseSpec(b); err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		return fmt.Errorf("writing spec: %w", err)
	}
	_, err = fmt.Fprintf(stdout, "commit=%s\nsha256=%s\n", commit, apispec.SHA256(b))
	return err
}

func watch(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	pinnedPath := fs.String("pinned", "", "the pinned spec")
	latestPath := fs.String("latest", "", "the latest spec")
	commit := fs.String("commit", "", "the commit of the latest spec")
	manifestPath := fs.String("manifest", "coverage.yaml", "the coverage manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pinnedPath == "" || *latestPath == "" || *commit == "" {
		return errors.New("watch: -pinned, -latest and -commit are required")
	}

	pinned, err := apispec.LoadSpec(*pinnedPath)
	if err != nil {
		return err
	}
	latest, err := apispec.LoadSpec(*latestPath)
	if err != nil {
		return err
	}
	m, err := apispec.LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	findings := apispec.Watch(pinned, latest, m, *commit)
	if findings == nil {
		findings = []apispec.Finding{}
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(findings)
}
