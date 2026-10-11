package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func configCmd(args []string) error {
	return checkConfig(args, os.Stdout)
}

func checkConfig(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("config requires check")
	}
	fs := flag.NewFlagSet("config check", flag.ContinueOnError)
	sources := fs.Bool("sources", false, "show field origins without configuration values")
	task := fs.String("task", "", "check the resolved configuration against this Task's recorded approval")
	dispatch := fs.String("dispatch", "", "check both current Task approval and the saved Dispatch instruction")
	if err := fs.Parse(args[1:]); err != nil {
		switch err.Error() {
		case "flag provided but not defined: -file", "flag provided but not defined: -user-file", "flag provided but not defined: -project-file":
			return fmt.Errorf("configuration file options have been removed; use Global taxion/config.yaml under the OS user configuration directory or cwd/.taxion/config.yaml, with TAXION_* environment overrides")
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("config check accepts no positional arguments")
	}
	if *task != "" && *dispatch != "" {
		return fmt.Errorf("config check selects either --task or --dispatch, not both")
	}
	userDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locate user configuration directory: %w", err)
	}
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate project configuration directory: %w", err)
	}
	layers, err := config.ReadFiles(userDir, projectDir)
	if err != nil {
		return err
	}
	environment, present, err := config.EnvironmentLayer(os.LookupEnv)
	if err != nil {
		return err
	}
	if present {
		layers = append(layers, environment)
	}
	resolved, err := config.Resolve(layers)
	if err != nil {
		return err
	}
	if *task != "" || *dispatch != "" {
		db, err := openWorkspace()
		if err != nil {
			return err
		}
		defer db.Close()
		if *dispatch != "" {
			if _, err := controller.CheckDispatchConfiguration(db, *dispatch, resolved.Config); err != nil {
				return err
			}
		} else {
			if _, err := controller.CheckConfiguration(db, *task, resolved.Config); err != nil {
				return err
			}
		}
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
	if *dispatch != "" {
		_, err = fmt.Fprintln(output, "Configuration matches the sourced Task approval and saved Dispatch instruction. Authentication, contract eligibility, and runtime configuration binding have not been verified.")
		return err
	}
	if *task != "" {
		_, err = fmt.Fprintln(output, "Configuration matches a sourced Task approval. Human identity, authentication, contract eligibility, and execution readiness have not been verified.")
		return err
	}
	_, err = fmt.Fprintln(output, "Configuration schema and required references are valid. Authentication, approval authority, contract eligibility, and adapter availability have not been verified.")
	return err
}
