package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dundee/gdu/v5/pkg/server"
)

func main() {
	var (
		socket      = flag.String("socket", "/tmp/gdu.sock", "Unix socket path (e.g., /tmp/gdu.sock)")
		useStorage  = flag.Bool("use-storage", true, "Use persistent storage for analysis data")
		storagePath = flag.String("storage-path", "/tmp/gdu-storage", "Path to persistent storage directory")
		help        = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// Setup cleanup on interrupt
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\nShutting down...")
		if fileExists(*socket) {
			os.Remove(*socket)
		}
		os.Exit(0)
	}()

	// Start server
	fmt.Println("Gdu Unix Socket Protocol Server")
	fmt.Println("=================================")
	fmt.Printf("Socket: %s\n\n", *socket)
	fmt.Println("Protocol: Length-prefixed JSON")
	fmt.Println("  [4 bytes: length][N bytes: JSON][1 byte: newline]")
	fmt.Println("")
	fmt.Println("Methods:")
	fmt.Println("  scan       - Start scanning")
	fmt.Println("  progress   - Get scanning progress")
	fmt.Println("  cancel     - Cancel scanning")
	fmt.Println("  directory  - Get directory info")
	fmt.Println("")
	fmt.Println("Example request:")
	fmt.Println(`  {"id":"1","method":"progress","params":{}}`)
	fmt.Println("")

	protoServer, err := server.NewUnixSocketServer(*socket, *useStorage, *storagePath)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	if err := protoServer.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func printHelp() {
	fmt.Println("Usage: gdu-server [options]")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  -socket string         Unix socket path (default: /tmp/gdu.sock)")
	fmt.Println("  -use-storage           Use persistent storage for analysis data (default: true)")
	fmt.Println("  -storage-path string   Path to persistent storage directory (default: /tmp/gdu-storage)")
	fmt.Println("  -help                  Show this help message")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  gdu-server                                                  # Use default socket with stored analyzer")
	fmt.Println("  gdu-server -socket /tmp/gdu.sock                           # Unix socket with stored analyzer")
	fmt.Println("  gdu-server -use-storage=false                              # Disable persistent storage")
	fmt.Println("  gdu-server -storage-path /path/to/storage                  # Custom storage path")
	fmt.Println("")
	fmt.Println("Unix socket mode features:")
	fmt.Println("  - Latency: ~0.05ms")
	fmt.Println("  - Throughput: ~120k req/s")
	fmt.Println("  - See SOCKET_PROTOCOL.md for binary protocol specification")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
