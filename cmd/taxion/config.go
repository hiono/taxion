package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/config"
)

func configCmd(args []string) error {
	return checkConfig(args, os.Stdout)
}

func checkConfig(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("config requires check --file YAML")
	}
	fs := flag.NewFlagSet("config check", flag.ContinueOnError)
	file := fs.String("file", "", "explicit YAML configuration path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("config check requires --file YAML and no positional arguments")
	}
	input, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}
	if _, err := config.Load(input); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "Configuration schema and required references are valid. Authentication, approval authority, contract eligibility, and adapter availability have not been verified.")
	return err
}
