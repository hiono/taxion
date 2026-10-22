package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
)

func rolePacketCommand(args []string, output io.Writer) error {
	if len(args) > 0 && args[0] == "read" {
		return rolePacketReadCommand(args[1:], output)
	}
	if len(args) > 0 && args[0] == "list" {
		return rolePacketListCommand(args[1:], output)
	}
	if len(args) == 0 || args[0] != "create" && args[0] != "verify" {
		return fmt.Errorf("usage: taxion role-packet {create|verify}")
	}
	fs := flag.NewFlagSet("role-packet "+args[0], flag.ContinueOnError)
	var diagnostics bytes.Buffer
	fs.SetOutput(&diagnostics)
	var fields core.RoleHandoffPacketFields
	var file, expectedHash string
	if args[0] == "create" {
		fs.StringVar(&fields.TaskID, "task", "", "Task ID")
		fs.StringVar(&fields.TaskHash, "task-hash", "", "canonical saved Task SHA-256")
		fs.StringVar(&fields.BaselineSHA, "baseline-sha", "", "full baseline Git commit hash")
		fs.StringVar(&fields.TargetTreeHash, "target-tree-hash", "", "full target Git tree hash")
		fs.StringVar(&fields.ScopeDigest, "scope-digest", "", "ApprovalBundle scope digest")
		fs.StringVar(&fields.AcceptanceDigest, "acceptance-digest", "", "ApprovalBundle acceptance digest")
		fs.StringVar(&fields.Role, "role", "", "CODER, VALIDATOR or REVIEWER")
		fs.StringVar(&fields.PreviousEvidenceHash, "previous-evidence-hash", "", "previous role evidence SHA-256 (required after Coder)")
		fs.Func("prohibit", "prohibited action; repeat for multiple actions", func(action string) error {
			fields.ProhibitedActions = append(fields.ProhibitedActions, action)
			return nil
		})
	} else {
		fs.StringVar(&file, "file", "", "role packet JSON file")
		fs.StringVar(&expectedHash, "expected-hash", "", "independently recorded packet SHA-256")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := io.WriteString(output, diagnostics.String())
			return err
		}
		// Other flag errors can contain caller-supplied values.
		return fmt.Errorf("taxion: invalid role-packet options")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("taxion: role-packet accepts no positional arguments")
	}
	var packet core.RoleHandoffPacket
	var err error
	if args[0] == "create" {
		fields.RequiredOutputSchema = "taxion.dispatch-result/v1"
		if fields.Role == "VALIDATOR" {
			fields.RequiredOutputSchema = "taxion.gate-report/v1"
		}
		packet, err = core.NewRoleHandoffPacket(fields)
	} else {
		if file == "" || expectedHash == "" {
			return fmt.Errorf("taxion: role-packet verify requires --file and --expected-hash")
		}
		data, readErr := os.ReadFile(file)
		if readErr != nil {
			return fmt.Errorf("taxion: cannot read role packet file")
		}
		packet, err = core.ReadRoleHandoffPacket(data, expectedHash)
	}
	if err != nil {
		return err
	}
	data, err := packet.MarshalJSON()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(data))
	return err
}
