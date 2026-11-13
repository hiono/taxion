package main

import (
	_ "embed"
	"fmt"
	"io"
)

//go:generate go run ./gencompletion

//go:embed completion.bash
var bashCompletion string

func completionCmd(args []string, output io.Writer) error {
	if len(args) != 1 || args[0] != "bash" {
		return fmt.Errorf("usage: taxion completion bash (other shells are not supported)")
	}
	_, err := io.WriteString(output, bashCompletion)
	return err
}
