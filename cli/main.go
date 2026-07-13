package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "deploy":
		err = cmdDeploy(args)
	case "redeploy":
		err = cmdRedeploy(args)
	case "list":
		err = cmdList()
	case "delete":
		err = cmdDelete(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`quick — deploy AI-generated HTML sites with zero config

Usage:
  quick deploy <folder> [--name <name>]   Deploy a site (zip + upload)
  quick redeploy <name> [folder]         Redeploy existing site
  quick list                             List known local sites
  quick delete <name>                    Delete a site

Environment:
  QUICK_SERVER   API base URL (default http://localhost:8080)

Config is stored in ~/.quick/config.json
`)
}
