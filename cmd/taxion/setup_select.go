package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func selectSetup(input *os.File, reader *bufio.Reader, output io.Writer, label string, choices []string, selected int) (choice string, err error) {
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return "", fmt.Errorf("cannot enable setup selection: %w", err)
	}
	defer func() {
		if restoreErr := term.Restore(int(input.Fd()), state); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("cannot restore terminal: %w", restoreErr))
		}
	}()
	return readSetupSelection(reader, output, label, choices, selected)
}

func readSetupSelection(reader *bufio.Reader, output io.Writer, label string, choices []string, selected int) (string, error) {
	if len(choices) == 0 || selected < 0 || selected >= len(choices) {
		return "", fmt.Errorf("invalid setup selection")
	}
	if _, err := fmt.Fprintf(output, "%s [%s] (Up/Down, Enter to select, Ctrl-C to cancel)\r\n", label, strings.Join(choices, " / ")); err != nil {
		return "", err
	}
	for {
		if _, err := fmt.Fprintf(output, "\r\x1b[2K> %s", choices[selected]); err != nil {
			return "", err
		}
		key, err := reader.ReadByte()
		if err != nil {
			return "", fmt.Errorf("setup selection ended; no files changed: %w", err)
		}
		switch key {
		case 3, 4:
			if _, err := fmt.Fprint(output, "\r\n"); err != nil {
				return "", err
			}
			return "", fmt.Errorf("setup cancelled; no files changed")
		case '\r', '\n':
			if _, err := fmt.Fprint(output, "\r\n"); err != nil {
				return "", err
			}
			return choices[selected], nil
		case 27:
			prefix, err := reader.ReadByte()
			if err != nil {
				return "", fmt.Errorf("setup selection ended; no files changed: %w", err)
			}
			if prefix != '[' && prefix != 'O' {
				continue
			}
			direction, err := reader.ReadByte()
			if err != nil {
				return "", fmt.Errorf("setup selection ended; no files changed: %w", err)
			}
			switch direction {
			case 'A':
				selected = (selected + len(choices) - 1) % len(choices)
			case 'B':
				selected = (selected + 1) % len(choices)
			}
		}
	}
}
