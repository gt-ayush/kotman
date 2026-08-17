package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kotman/internal/db"
	"kotman/internal/tunnel"
	"kotman/protocol"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

var TunnelManager *tunnel.Manager

// Device now holds the Yamux session and JSON encoder instead of a WebSocket[cite: 1]
type Device struct {
	ID       string
	Status   string
	LastSeen time.Time
	Session  *yamux.Session
	Encoder  *json.Encoder

	PendingMutex sync.Mutex
	Pending      map[string]chan protocol.Message
}

var (
	devices = make(map[string]*Device)
	mutex   sync.Mutex // Protects the devices map
)

func main() {
	if err := db.InitDB("kotman.db"); err != nil {
		log.Fatal("DB Init Error:", err)
	}

	// Reset all devices to OFFLINE on server startup[cite: 1]
	db.DB.Exec("UPDATE devices SET status = 'OFFLINE'")

	// Start background timeout monitor[cite: 1]
	go monitorTimeouts()

	// 1. Phase 6A: Start the dedicated Yamux TCP listener for Agents on port 8082
	go startAgentTCPServer()

	TunnelManager = tunnel.NewManager(func(deviceID, streamID string, localPort int) error {
		mutex.Lock()
		dev, ok := devices[deviceID]
		mutex.Unlock()

		if !ok || dev.Status != "ONLINE" {
			return fmt.Errorf("device offline")
		}

		// Write to the Yamux control stream encoder instead of WebSocket[cite: 1]
		return dev.Encoder.Encode(protocol.Message{
			Type:      protocol.MsgTunnelReq,
			RequestID: streamID,
			Args:      map[string]string{"local_port": strconv.Itoa(localPort)},
		})
	})

	// Auto-start existing tunnels from DB on boot[cite: 1]
	rows, _ := db.DB.Query("SELECT tunnel_id, device_id, vps_port, local_port FROM tunnels")
	for rows.Next() {
		var tID, dID string
		var vp, lp int
		rows.Scan(&tID, &dID, &vp, &lp)
		TunnelManager.StartListener(tID, dID, vp, lp)
	}

	// 2. Local Admin API for the CLI[cite: 1]
	http.HandleFunc("/api/ps", apiPS)
	http.HandleFunc("/api/rename", apiRename)
	http.HandleFunc("/api/inspect", apiInspect)
	http.HandleFunc("/api/exec", apiExec)

	// 3. Tunnels CLI[cite: 1]
	http.HandleFunc("/api/tunnel/data", handleTunnelData)
	http.HandleFunc("/api/tunnel/create", apiTunnelCreate)
	http.HandleFunc("/api/tunnel/ls", apiTunnelLs)
	http.HandleFunc("/api/tunnel/rm", apiTunnelRm)

	log.Println("Listening for CLI and Tunnel Data on 127.0.0.1:8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

// --- PHASE 6A: YAMUX TCP SERVER ---

func startAgentTCPServer() {
	listener, err := net.Listen("tcp", ":8082")
	if err != nil {
		log.Fatalf("Failed to start agent TCP listener: %v", err)
	}
	log.Println("Listening for Agents on 0.0.0.0:8082 (TCP/Yamux)")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("TCP accept error:", err)
			continue
		}
		go handleAgentYamuxConnection(conn)
	}
}

func handleAgentYamuxConnection(conn net.Conn) {
	// 1. Wrap the raw TCP connection with Yamux
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Println("Yamux setup error:", err)
		conn.Close()
		return
	}

	// 2. Accept the very first stream from the Agent (Stream 0) for Control
	controlStream, err := session.Accept()
	if err != nil {
		log.Println("Failed to accept control stream:", err)
		session.Close()
		return
	}

	decoder := json.NewDecoder(controlStream)
	encoder := json.NewEncoder(controlStream)

	var msg protocol.Message
	if err := decoder.Decode(&msg); err != nil {
		session.Close()
		return
	}

	var deviceID string
	if msg.Type == protocol.MsgHello {
		deviceID = msg.DeviceID
		nickname, _ := db.RegisterOrConnect(deviceID, msg.Version)

		mutex.Lock()
		devices[deviceID] = &Device{
			ID:       deviceID,
			Status:   "ONLINE",
			LastSeen: time.Now(),
			Session:  session,
			Encoder:  encoder,
			Pending:  make(map[string]chan protocol.Message),
		}
		mutex.Unlock()

		log.Printf("Agent Connected via Yamux: %s (%s)", nickname, deviceID)
		encoder.Encode(protocol.Message{Type: protocol.MsgAuthOK})
	} else {
		session.Close()
		return
	}

	// Disconnect cleanup
	defer func() {
		if deviceID != "" {
			db.DB.Exec("UPDATE devices SET status = 'OFFLINE' WHERE device_id = ?", deviceID)

			mutex.Lock()
			delete(devices, deviceID)
			mutex.Unlock()
		}
		session.Close()
	}()

	// 3. The Control Loop (Replaces the old WebSocket loop)[cite: 1]
	for {
		var m protocol.Message
		if err := decoder.Decode(&m); err != nil {
			break
		}

		switch m.Type {
		case protocol.MsgHeartbeat:
			now := time.Now()
			db.DB.Exec("UPDATE devices SET last_seen = ?, status = 'ONLINE' WHERE device_id = ?", now.Format(time.RFC3339), deviceID)

			mutex.Lock()
			if dev, ok := devices[deviceID]; ok {
				dev.LastSeen = now
			}
			mutex.Unlock()

			encoder.Encode(protocol.Message{Type: protocol.MsgHeartbeatAck})

		case protocol.MsgExecResult:
			mutex.Lock()
			dev, ok := devices[deviceID]
			mutex.Unlock()

			if ok {
				dev.PendingMutex.Lock()
				if ch, pendingOk := dev.Pending[m.RequestID]; pendingOk {
					ch <- m
					delete(dev.Pending, m.RequestID)
				}
				dev.PendingMutex.Unlock()
			}
		}
	}
}

func monitorTimeouts() {
	for {
		time.Sleep(5 * time.Second)
		timeoutThreshold := time.Now().Add(-15 * time.Second).Format(time.RFC3339)
		db.DB.Exec("UPDATE devices SET status = 'OFFLINE' WHERE last_seen < ? AND status = 'ONLINE'", timeoutThreshold)
	}
}

// --- ADMIN API Handlers for CLI ---

func apiPS(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query("SELECT device_id, nickname, status, first_seen, last_seen, agent_version FROM devices")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var devs []map[string]string
	for rows.Next() {
		var id, name, status, first, last, ver string
		rows.Scan(&id, &name, &status, &first, &last, &ver)
		devs = append(devs, map[string]string{
			"device_id": id, "nickname": name, "status": status,
			"first_seen": first, "last_seen": last, "agent_version": ver,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devs)
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
	dev, ok := devices[deviceID]
	mutex.Unlock()

	if !ok {
		http.Error(w, "Device socket connection not found", http.StatusNotFound)
		return
	}

	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	respChan := make(chan protocol.Message, 1)

	dev.PendingMutex.Lock()
	dev.Pending[reqID] = respChan
	dev.PendingMutex.Unlock()

	// Send command down the Yamux stream to the Agent
	dev.Encoder.Encode(protocol.Message{
		Type:      protocol.MsgExec,
		RequestID: reqID,
		Operation: req.Operation,
		Args:      req.Args,
	})

	// Wait for Agent response with a 10-second timeout[cite: 1]
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

// --- Handlers ---

// Side-channel WebSocket for data streams (Kept temporarily until Phase 6B)
func handleTunnelData(w http.ResponseWriter, r *http.Request) {
	streamID := r.URL.Query().Get("stream_id")
	if streamID == "" {
		http.Error(w, "missing stream_id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	if !TunnelManager.RegisterDataStream(streamID, conn) {
		conn.Close() // invalid or expired stream_id
	}
}

func apiTunnelCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target    string `json:"target"`
		VPSPort   int    `json:"vps_port"`
		LocalPort int    `json:"local_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var deviceID string
	err := db.DB.QueryRow("SELECT device_id FROM devices WHERE nickname = ?", req.Target).Scan(&deviceID)
	if err != nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	tunnelID := fmt.Sprintf("t-%d", time.Now().Unix())

	_, err = db.DB.Exec("INSERT INTO tunnels (tunnel_id, device_id, vps_port, local_port) VALUES (?, ?, ?, ?)",
		tunnelID, deviceID, req.VPSPort, req.LocalPort)
	if err != nil {
		http.Error(w, "Failed to save tunnel to DB", http.StatusInternalServerError)
		return
	}

	if err := TunnelManager.StartListener(tunnelID, deviceID, req.VPSPort, req.LocalPort); err != nil {
		db.DB.Exec("DELETE FROM tunnels WHERE tunnel_id = ?", tunnelID) // Rollback[cite: 1]
		http.Error(w, "Failed to bind port on VPS: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"tunnel_id": tunnelID, "status": "created"})
}

func apiTunnelLs(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT t.tunnel_id, d.nickname, t.vps_port, t.local_port 
		FROM tunnels t 
		JOIN devices d ON t.device_id = d.device_id
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tunnels []map[string]any
	for rows.Next() {
		var tID, pc string
		var vp, lp int
		rows.Scan(&tID, &pc, &vp, &lp)
		tunnels = append(tunnels, map[string]any{
			"id": tID, "pc": pc, "vps_port": vp, "local_port": lp,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tunnels)
}

func apiTunnelRm(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	json.NewDecoder(r.Body).Decode(&req)
	tunnelID := req["tunnel_id"]

	db.DB.Exec("DELETE FROM tunnels WHERE tunnel_id = ?", tunnelID)
	TunnelManager.StopListener(tunnelID)
	w.WriteHeader(http.StatusOK)
}