package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

func currentBuildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown (build information unavailable)"
	}
	return buildVersion(info)
}

func buildVersion(info *debug.BuildInfo) string {
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision != "" {
		version += " revision=" + revision
	}
	if modified {
		version += " modified=true"
	}
	if version == "(devel)" {
		version += " (revision unavailable)"
	}
	return version
}

func printHelp(output io.Writer) error {
	title, rest, _ := strings.Cut(usage, "\n")
	_, err := fmt.Fprintf(output, "%s\nVersion: %s\n%s", title, currentBuildVersion(), rest)
	return err
}
