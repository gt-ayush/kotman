package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"kotman/internal/db"
	"kotman/protocol"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func main() {
	if err := db.InitDB("kotman.db"); err != nil {
		log.Fatal("DB Init Error:", err)
	}

	// Reset all devices to OFFLINE on server startup
	db.DB.Exec("UPDATE devices SET status = 'OFFLINE'")

	// Start background timeout monitor
	go monitorTimeouts()

	// 1. Public API for PC Agents
	http.HandleFunc("/ws", handleAgentConnection)
	go func() {
		log.Println("Listening for Agents on 0.0.0.0:8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	// 2. Local Admin API for the CLI
	http.HandleFunc("/api/ps", apiPS)
	http.HandleFunc("/api/rename", apiRename)
	http.HandleFunc("/api/inspect", apiInspect)
	log.Println("Listening for CLI on 127.0.0.1:8081")
	log.Fatal(http.ListenAndServe("127.0.0.1:8081", nil))
}

func handleAgentConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil { return }
	var deviceID string

	defer func() {
		if deviceID != "" {
			db.DB.Exec("UPDATE devices SET status = 'OFFLINE' WHERE device_id = ?", deviceID)
		}
		conn.Close()
	}()

	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil { break }

		switch msg.Type {
		case protocol.MsgHello:
			deviceID = msg.DeviceID
			nickname, _ := db.RegisterOrConnect(deviceID, msg.Version)
			log.Printf("Agent Connected: %s (%s)", nickname, deviceID)
			conn.WriteJSON(protocol.Message{Type: protocol.MsgAuthOK})

		case protocol.MsgHeartbeat:
			now := time.Now().Format(time.RFC3339)
			db.DB.Exec("UPDATE devices SET last_seen = ?, status = 'ONLINE' WHERE device_id = ?", now, deviceID)
			conn.WriteJSON(protocol.Message{Type: protocol.MsgHeartbeatAck})
		}
	}
}

// monitorTimeouts automatically marks devices offline if no heartbeat in 15s
func monitorTimeouts() {
	for {
		time.Sleep(5 * time.Second)
		timeoutThreshold := time.Now().Add(-15 * time.Second).Format(time.RFC3339)
		db.DB.Exec("UPDATE devices SET status = 'OFFLINE' WHERE last_seen < ? AND status = 'ONLINE'", timeoutThreshold)
	}
}

func apiPS(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query("SELECT device_id, nickname, status, first_seen, last_seen, agent_version FROM devices")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var devices []map[string]string
	for rows.Next() {
		var id, name, status, first, last, ver string
		rows.Scan(&id, &name, &status, &first, &last, &ver)
		devices = append(devices, map[string]string{
			"device_id": id, "nickname": name, "status": status,
			"first_seen": first, "last_seen": last, "agent_version": ver,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}

func apiRename(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	json.NewDecoder(r.Body).Decode(&req)

	_, err := db.DB.Exec("UPDATE devices SET nickname = ? WHERE nickname = ?", req["new_name"], req["old_name"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func apiInspect(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	var id, nickname, status, first, last, ver string
	err := db.DB.QueryRow("SELECT device_id, nickname, status, first_seen, last_seen, agent_version FROM devices WHERE nickname = ?", name).
		Scan(&id, &nickname, &status, &first, &last, &ver)

	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"device_id": id, "nickname": nickname, "status": status,
		"first_seen": first, "last_seen": last, "agent_version": ver,
	})
}
// --- ADMIN API Handlers for CLI ---
// Implementation omitted for brevity, but they just run standard SELECT/UPDATE
// queries on the SQLite DB and return JSON. E.g. UPDATE devices SET nickname=?

//---
// 1. Update the Device struct to hold pending requests
type Device struct {
	ID       string
	Status   string
	LastSeen time.Time
	Conn     *websocket.Conn
	
	PendingMutex sync.Mutex
	Pending      map[string]chan protocol.Message
}

// 2. In handleAgentConnection, initialize the map and handle MsgExecResult:
// devices[deviceID] = &Device{ ... Pending: make(map[string]chan protocol.Message) }

// Inside the read loop:
case protocol.MsgExecResult:
	dev := devices[deviceID]
	dev.PendingMutex.Lock()
	if ch, ok := dev.Pending[msg.RequestID]; ok {
		ch <- msg // Route response back to the waiting HTTP handler
		delete(dev.Pending, msg.RequestID)
	}
	dev.PendingMutex.Unlock()

// 3. Add the new Admin API Handler (Don't forget to register it in main(): http.HandleFunc("/api/exec", apiExec))
func apiExec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target    string            `json:"target"`
		Operation string            `json:"operation"`
		Args      map[string]string `json:"args"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var deviceID, status string
	err := db.DB.QueryRow("SELECT device_id, status FROM devices WHERE nickname = ?", req.Target).Scan(&deviceID, &status)
	
	if err != nil || status != "ONLINE" {
		http.Error(w, "Device offline or not found", http.StatusNotFound)
		return
	}

	mutex.Lock()
	dev := devices[deviceID]
	mutex.Unlock()

	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	respChan := make(chan protocol.Message, 1)

	dev.PendingMutex.Lock()
	dev.Pending[reqID] = respChan
	dev.PendingMutex.Unlock()

	// Send to Agent
	dev.Conn.WriteJSON(protocol.Message{
		Type:      protocol.MsgExec,
		RequestID: reqID,
		Operation: req.Operation,
		Args:      req.Args,
	})

	// Wait for Agent response with a 10-second timeout
	select {
	case result := <-respChan:
		db.LogOperation(deviceID, req.Operation, result.Success, result.Error)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	case <-time.After(10 * time.Second):
		dev.PendingMutex.Lock()
		delete(dev.Pending, reqID)
		dev.PendingMutex.Unlock()
		
		db.LogOperation(deviceID, req.Operation, false, "timeout")
		http.Error(w, `{"error": "timeout"}`, http.StatusGatewayTimeout)
	}
}
