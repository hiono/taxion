// Command taxion is the human-facing CLI for the Taxion control plane.
//
// Day2 scope: local-first, deterministic, no network, no SDK dependency.
// Future: thin client over Taxion Server (Day3+) with ACP support.
package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `taxion — Evidence-governed control plane for coding agents

Usage:
  taxion <command> [options]

Commands:
  init                Initialize a local workspace
  task create         Create a task (--title required)
  dispatch            Dispatch a task (--task required)
  review              Review a coder output (--sha required)
  resume              Resume a session (--session required)

Options:
  -h, --help          Show help
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		fmt.Println("taxion: initialized (day2 local stub)")
	case "task":
		taskCmd(os.Args[2:])
	case "dispatch":
		fs := flag.NewFlagSet("dispatch", flag.ExitOnError)
		task := fs.String("task", "", "task id")
		_ = fs.Parse(os.Args[2:])
		if *task == "" {
			fmt.Fprintln(os.Stderr, "dispatch: --task is required")
			os.Exit(2)
		}
		fmt.Printf("taxion: dispatched task %s (day2 local stub)\n", *task)
	case "review":
		fs := flag.NewFlagSet("review", flag.ExitOnError)
		sha := fs.String("sha", "", "coder output SHA")
		_ = fs.Parse(os.Args[2:])
		if *sha == "" {
			fmt.Fprintln(os.Stderr, "review: --sha is required (SHA-001)")
			os.Exit(2)
		}
		fmt.Printf("taxion: review bound to SHA %s (day2 local stub)\n", *sha)
	case "resume":
		fs := flag.NewFlagSet("resume", flag.ExitOnError)
		session := fs.String("session", "", "session id")
		_ = fs.Parse(os.Args[2:])
		if *session == "" {
			fmt.Fprintln(os.Stderr, "resume: --session is required")
			os.Exit(2)
		}
		fmt.Printf("taxion: resumed session %s (day2 local stub)\n", *session)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func taskCmd(args []string) {
	if len(args) < 1 || args[0] != "create" {
		fmt.Fprintf(os.Stderr, "usage: taxion task create --title \"…\"\n")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("task create", flag.ExitOnError)
	title := fs.String("title", "", "task title")
	_ = fs.Parse(args[1:])
	if *title == "" {
		fmt.Fprintln(os.Stderr, "task create: --title is required")
		os.Exit(2)
	}
	fmt.Printf("taxion: task created %q (day2 local stub)\n", *title)
}
