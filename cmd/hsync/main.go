package main

import (
	"fmt"
	"hsync/internal/client"
	"hsync/internal/server"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	args := os.Args[2:]

	switch subcommand {
	case "client":
		client.Run(args)
	case "server":
		server.Run(args)
	default:
		fmt.Printf("Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: hsync <command> [arguments]")
	fmt.Println("Commands:")
	fmt.Println("  client    Run the sync client")
	fmt.Println("  server    Run the sync server")
}
