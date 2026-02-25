package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JupiterMetaLabs/JMDN-FastSync/tests/example"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\nShutting down...")
		cancel()
	}()

	fmt.Println("=== JMDN-FastSync Interactive CLI ===")
	fmt.Println("Commands:")
	fmt.Println("  listen <port>           - Start listening for PriorSync messages")
	fmt.Println("  send <msg>              - Send a PriorSync message to a peer")
	fmt.Println("  status                  - Show current node status")
	fmt.Println("  help                    - Show this help message")
	fmt.Println("  quit                    - Exit the CLI")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	var node *example.Node

	for {
		select {
		case <-ctx.Done():
			if node != nil {
				node.Stop()
			}
			return
		default:
		}

		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		parts := strings.Fields(input)
		cmd := parts[0]

		switch cmd {
		case "listen":
			if node != nil {
				fmt.Println("Already listening. Stop first with 'quit'")
				continue
			}

			port := "0" // Random port
			if len(parts) > 1 {
				port = parts[1]
			}

			var err error
			node, err = example.StartListening(ctx, port, 2)
			if err != nil {
				fmt.Printf("Error starting listener: %v\n", err)
				continue
			}

			fmt.Printf("Listening on: %s\n", node.GetAddress())
			fmt.Printf("Peer ID: %s\n", node.GetPeerID())

		case "send":
			if len(parts) < 3 {
				fmt.Println("Usage: send <peer-multiaddr> <state>")
				fmt.Println("Example: send /ip4/127.0.0.1/tcp/4001/p2p/QmPeerID SYNC_REQUEST")
				continue
			}

			if node == nil {
				fmt.Println("Start listening first with 'listen <port>'")
				continue
			}

			sendAddrs := parts[1]
			state := parts[2]

			if err := example.SendMessage(ctx, node, sendAddrs, state); err != nil {
				fmt.Printf("Error sending message: %v\n", err)
			} else {
				fmt.Println("Message sent successfully!")
			}

		case "status":
			if node == nil {
				fmt.Println("Node not running. Use 'listen <port>' to start.")
			} else {
				fmt.Printf("Status: Running\n")
				fmt.Printf("Address: %s\n", node.GetAddress())
				fmt.Printf("Peer ID: %s\n", node.GetPeerID())
			}

		case "help":
			fmt.Println("Commands:")
			fmt.Println("  listen <port>           - Start listening (port optional, default: random)")
			fmt.Println("  send <peer-addr> <msg>  - Send PriorSync message")
			fmt.Println("  status                  - Show node status")
			fmt.Println("  quit                    - Exit")

		case "quit", "exit":
			if node != nil {
				fmt.Println("Stopping node...")
				node.Stop()
			}
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", cmd)
		}
	}
}
