package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
)

//go:embed ready.txt
var readyInstructions string

var readyPrompt = readyInstructions + "\n## Goal-oriented execution policy\n\n" +
	goalExecutionPolicy + "\n" + goalRolePolicy + "\n"

func readyCmd(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("ready", flag.ContinueOnError)
	flags.SetOutput(output)
	install := flags.Bool("install-skill", false, "install the bundled versioned skill in ~/.agents/skills/taxion")
	update := flags.Bool("update-skill", false, "explicitly replace an intact Taxion-managed skill with the bundled version")
	check := flags.Bool("check-skill", false, "check installed skill version, CLI compatibility and integrity as JSON; no writes")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("ready accepts no arguments; discuss the request with your coding agent")
	}
	count := 0
	for _, enabled := range []bool{*install, *update, *check} {
		if enabled {
			count++
		}
	}
	if count > 1 {
		return fmt.Errorf("ready: choose only one of --install-skill, --update-skill or --check-skill")
	}
	if *check {
		return checkReadySkill(output)
	}
	if *install || *update {
		if err := installReadySkill(*update, output); err != nil {
			return err
		}
	}
	_, err := io.WriteString(output, readyPrompt)
	return err
}
