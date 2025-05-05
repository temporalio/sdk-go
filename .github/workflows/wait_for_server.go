package main

import (
	"log"
	"time"

	"go.temporal.io/sdk/client"
)

func main() {
	// We'll try to connect every two seconds for 5 minutes
	log.Print("Waiting for server availability")
	var lastErr error
	for start := time.Now(); time.Since(start) < 5*time.Minute; time.Sleep(2 * time.Second) {
		_, lastErr = client.Dial(client.Options{})
		if lastErr == nil {
			log.Print("Connected to server")
			return
		}
	}
	log.Fatalf("Timeout waiting for server, last error: %v", lastErr)
}
