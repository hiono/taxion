package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
)

//go:embed ready.txt
var readyPrompt string

func readyCmd(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("ready", flag.ContinueOnError)
	flags.SetOutput(output)
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("ready accepts no arguments; discuss the request with your coding agent")
	}
	_, err := io.WriteString(output, readyPrompt)
	return err
}
