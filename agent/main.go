package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"kotman/protocol"

	"github.com/gorilla/websocket"
	"github.com/kardianos/service"
)

// In a real deployment, you'd read this from config.json
const serverURL = "ws://127.0.0.1:8080/ws"
const agentVersion = "0.1.0"

var writeMutex sync.Mutex

// program implements service.Interface
type program struct {
	exit chan struct{}
}

func main() {
	svcConfig := &service.Config{
		Name:        "KotmanAgent",
		DisplayName: "Kotman Agent",
		Description: "Background management agent for Kotman VPS",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}

	// Setup logger for the service (writes to Event Viewer on Windows, Syslog on Linux)
	logger, err := s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}

	// Handle CLI commands (install, uninstall, start, stop)
	if len(os.Args) > 1 {
		err = service.Control(s, os.Args[1])
		if err != nil {
			log.Fatalf("Valid actions: %q\n%v", service.ControlAction, err)
		}
		return
	}

	// Start the service
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	
	// Start the main agent loop in the background so Start() can return quickly
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	// Signal the running goroutines to shut down gracefully
	close(p.exit)
	<-time.After(time.Second) // Give it a moment to clean up
	return nil
}

func (p *program) run() {
	deviceID := getDeviceID()
	log.Printf("Starting Kotman Agent with Device ID: %s", deviceID)

	// Reconnect Loop
	for {
		select {
		case <-p.exit:
			return
		default:
		}

		err := p.connectToServer(deviceID)
		if err != nil {
			log.Printf("Disconnected: %v. Retrying in 5 seconds...", err)
			
			// Wait 5 seconds before retrying, but allow immediate exit if service is stopped
			select {
			case <-time.After(5 * time.Second):
			case <-p.exit:
				return
			}
		}
	}
}

func (p *program) connectToServer(deviceID string) error {
	log.Printf("Connecting to %s...", serverURL)
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// If the service is stopped, close the connection immediately to unblock ReadJSON
	go func() {
		select {
		case <-p.exit:
			conn.Close()
		}
	}()

	// Send HELLO
	err = sendMsg(conn, protocol.Message{
		Type:     protocol.MsgHello,
		DeviceID: deviceID,
		Version:  agentVersion,
	})
	if err != nil {
		return err
	}

	// Start Heartbeat loop
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ticker.C:
				sendMsg(conn, protocol.Message{Type: protocol.MsgHeartbeat})
			case <-p.exit:
				return
			}
		}
	}()

	// Main Read Loop
	for {
		var msg protocol.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			return err // Network error or connection closed; return triggers reconnect
		}

		switch msg.Type {
		case protocol.MsgAuthOK:
			log.Println("Authenticated with VPS successfully.")
		case protocol.MsgExec:
			go func(req protocol.Message) {
				res := executeOperation(req)
				sendMsg(conn, res)
			}(msg)

		// dispatcher loop for tunnels:
		case protocol.MsgTunnelReq:
			go handleTunnelReq(msg)
		}
	}
}

func executeOperation(req protocol.Message) protocol.Message {
	res := protocol.Message{
		Type:      protocol.MsgExecResult,
		RequestID: req.RequestID,
		Success:   true,
		Result:    make(map[string]string),
	}

	switch req.Operation {
	case "status":
		res.Result["status"] = "ONLINE"
		res.Result["agent_version"] = agentVersion
	case "system-info":
		res.Result["os"] = runtime.GOOS
		res.Result["arch"] = runtime.GOARCH
		res.Result["cpus"] = strconv.Itoa(runtime.NumCPU())
	case "run-task":
		taskName := req.Args["task"]
		if taskName == "update-dashboard" {
			time.Sleep(2 * time.Second)
			res.Result["output"] = "Dashboard updated successfully."
		} else {
			res.Success = false
			res.Error = "Unknown task: " + taskName
		}
	default:
		res.Success = false
		res.Error = "operation not permitted"
	}
	return res
}

func sendMsg(conn *websocket.Conn, msg protocol.Message) error {
	writeMutex.Lock()
	defer writeMutex.Unlock()
	return conn.WriteJSON(msg)
}

// getDeviceID handles Phase 4 Persistent Identity rules
func getDeviceID() string {
	var dataDir string
	if runtime.GOOS == "windows" {
		dataDir = filepath.Join(os.Getenv("ProgramData"), "Kotman")
	} else {
		dataDir = "/var/lib/kotman"
	}

	os.MkdirAll(dataDir, 0755)
	idFile := filepath.Join(dataDir, "device-id")

	// Try to read existing ID
	b, err := os.ReadFile(idFile)
	if err == nil && len(b) > 0 {
		return string(b)
	}

	// Generate new ID if not found
	newID := make([]byte, 16)
	rand.Read(newID)
	idStr := hex.EncodeToString(newID)

	os.WriteFile(idFile, []byte(idStr), 0644)
	return idStr
}



// Handler function
func handleTunnelReq(req protocol.Message) {
    streamID := req.RequestID
    localPort, _ := strconv.Atoi(req.Args["local_port"])

    // 1. Dial Local Service
    tcp, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
    if err != nil {
        log.Printf("TunnelReq[%s]: failed to dial local port %d: %v", streamID, localPort, err)
        return
    }

    // 2. Dial VPS side-channel
    // Switch ws:// to wss:// if using TLS in production
    wsURL := fmt.Sprintf("ws://127.0.0.1:8080/api/tunnel/data?stream_id=%s", streamID)
    ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        log.Printf("TunnelReq[%s]: failed to dial VPS side-channel: %v", streamID, err)
        tcp.Close()
        return
    }

    // 3. Link them!
    go tunnel.Pump(ws, tcp)
}
