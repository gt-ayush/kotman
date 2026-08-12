package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"kotman/protocol"
	"github.com/gorilla/websocket"
)

const HeartbeatTimeout = 15 * time.Second

type Device struct {
	ID       string
	Status   string
	LastSeen time.Time
	Conn     *websocket.Conn
}

var (
	devices = make(map[string]*Device)
	mutex   = &sync.Mutex{}
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func main() {
	http.HandleFunc("/ws", handleConnection)
	
	// Background task to check for dead connections and print the "ps" table
	go monitorDevices()

	fmt.Println("Kotman VPS Server running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}

	var deviceID string

	defer func() {
		if deviceID != "" {
			mutex.Lock()
			if dev, ok := devices[deviceID]; ok {
				dev.Status = "OFFLINE"
			}
			mutex.Unlock()
		}
		conn.Close()
	}()

	for {
		var msg protocol.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			break // Connection closed or error
		}

		mutex.Lock()
		switch msg.Type {
		case protocol.MsgHello:
			deviceID = msg.DeviceID
			devices[deviceID] = &Device{
				ID:       deviceID,
				Status:   "ONLINE",
				LastSeen: time.Now(),
				Conn:     conn,
			}
			conn.WriteJSON(protocol.Message{Type: protocol.MsgAuthOK})
			log.Printf("Device %s authenticated.", deviceID)

		case protocol.MsgHeartbeat:
			if dev, ok := devices[deviceID]; ok {
				dev.LastSeen = time.Now()
				dev.Status = "ONLINE"
			}
			conn.WriteJSON(protocol.Message{Type: protocol.MsgHeartbeatAck})
		}
		mutex.Unlock()
	}
}

func monitorDevices() {
	for {
		time.Sleep(5 * time.Second)
		
		fmt.Print("\033[H\033[2J") // Clear screen for dashboard effect
		fmt.Printf("%-20s %-10s %-15s\n", "NAME/ID", "STATUS", "LAST SEEN")
		fmt.Println("------------------------------------------------")
		
		mutex.Lock()
		now := time.Now()
		for id, dev := range devices {
			if now.Sub(dev.LastSeen) > HeartbeatTimeout && dev.Status == "ONLINE" {
				dev.Status = "OFFLINE"
				dev.Conn.Close()
			}
			
			seenAgo := now.Sub(dev.LastSeen).Round(time.Second)
			fmt.Printf("%-20s %-10s %-15s\n", id[:8]+"...", dev.Status, seenAgo.String()+" ago")
		}
		mutex.Unlock()
	}
}
