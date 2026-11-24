package main

import (
	"context"
	"os"

	"github.com/hiono/taxion/internal/cli"
)

var (
	releaseVersion  = "dev"
	releaseRevision = "unknown"
	releaseBuiltAt  = "unknown"
	releaseChannel  = "development"
)

func main() {
	version := cli.VersionInfo{
		Version:  releaseVersion,
		Revision: releaseRevision,
		BuiltAt:  releaseBuiltAt,
		Channel:  releaseChannel,
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, version))
}
