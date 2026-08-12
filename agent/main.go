package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"time"

	"kotman/protocol"
	"github.com/gorilla/websocket"
)

const ServerURL = "ws://localhost:8080/ws"

func main() {
	deviceID := getOrCreateDeviceID()
	log.Printf("Kotman Agent starting... Device ID: %s", deviceID)

	// Infinite reconnect loop (satisfies Tests 4 & 5)
	for {
		err := connectAndRun(deviceID)
		if err != nil {
			log.Printf("Disconnected: %v. Retrying in 5 seconds...", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func connectAndRun(deviceID string) error {
	log.Printf("Connecting to %s...", ServerURL)
	conn, _, err := websocket.DefaultDialer.Dial(ServerURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 1. Send HELLO
	hello := protocol.Message{
		Type:     protocol.MsgHello,
		DeviceID: deviceID,
		Version:  "0.1.0",
	}
	if err := conn.WriteJSON(hello); err != nil {
		return err
	}

	// 2. Start Heartbeat goroutine
	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				conn.WriteJSON(protocol.Message{Type: protocol.MsgHeartbeat})
			}
		}
	}()

	// 3. Read loop
	for {
		var msg protocol.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			return err // Breaks read loop, triggers reconnect in main()
		}

		if msg.Type == protocol.MsgAuthOK {
			log.Println("Authenticated with VPS successfully.")
		}
		// We silently consume HEARTBEAT_ACK here to verify the server is alive
	}
}

// getOrCreateDeviceID ensures the device has a permanent identity (Test 3)
func getOrCreateDeviceID() string {
	const filename = "device_id.txt"
	data, err := os.ReadFile(filename)
	if err == nil && len(data) > 0 {
		return string(data)
	}

	bytes := make([]byte, 16)
	rand.Read(bytes)
	newID := hex.EncodeToString(bytes)
	
	os.WriteFile(filename, []byte(newID), 0600)
	return newID
}
