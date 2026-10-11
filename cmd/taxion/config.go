package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/hiono/taxion/internal/config"
)

func configCmd(args []string) error {
	return checkConfig(args, os.Stdout)
}

func checkConfig(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("config requires check")
	}
	fs := flag.NewFlagSet("config check", flag.ContinueOnError)
	file := fs.String("file", "", "explicit YAML configuration path")
	userFile := fs.String("user-file", "", "optional lower-priority user YAML settings")
	projectFile := fs.String("project-file", "", "optional project YAML settings overriding user settings")
	sources := fs.Bool("sources", false, "show field origins without configuration values")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("config check accepts no positional arguments")
	}
	var userDir string
	if *userFile == "" {
		var err error
		userDir, err = os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("locate user configuration directory: %w", err)
		}
	}
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate project configuration directory: %w", err)
	}
	layers, err := config.ReadFiles(userDir, projectDir, config.Files{
		User: *userFile, Project: *projectFile, Invocation: *file,
	})
	if err != nil {
		return err
	}
	resolved, err := config.Resolve(layers)
	if err != nil {
		return err
	}
	if *sources {
		var names []string
		for name := range resolved.Sources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, err := fmt.Fprintf(output, "%s: %s\n", name, resolved.Sources[name]); err != nil {
				return err
			}
		}
	}
	_, err = fmt.Fprintln(output, "Configuration schema and required references are valid. Authentication, approval authority, contract eligibility, and adapter availability have not been verified.")
	return err
}
