package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"kotman/internal/tunnel"
	"kotman/protocol"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/kardianos/service"
)

// Phase 6A: Yamux TCP Listener for control
const controlAddress = "127.0.0.1:8082" 

// Phase 6A: Temporary WebSocket Side-Channel (until Phase 6B)
const tunnelDataURL = "ws://127.0.0.1:8081/api/tunnel/data" 

const agentVersion = "v0.1.2" // Bumped version for Phase 6A

var writeMutex sync.Mutex

// program implements service.Interface[cite: 2]
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

	// Setup logger for the service (writes to Event Viewer on Windows, Syslog on Linux)[cite: 2]
	logger, err := s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}

	// Handle CLI commands (install, uninstall, start, stop)[cite: 2]
	if len(os.Args) > 1 {
		err = service.Control(s, os.Args[1])
		if err != nil {
			log.Fatalf("Valid actions: %q\n%v", service.ControlAction, err)
		}
		return
	}

	// Start the service[cite: 2]
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	
	// Start the main agent loop in the background so Start() can return quickly[cite: 2]
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	// Signal the running goroutines to shut down gracefully[cite: 2]
	close(p.exit)
	<-time.After(time.Second) // Give it a moment to clean up[cite: 2]
	return nil
}

func (p *program) run() {
	deviceID := getDeviceID()
	log.Printf("Starting Kotman Agent with Device ID: %s", deviceID)

	// Reconnect Loop[cite: 2]
	for {
		select {
		case <-p.exit:
			return
		default:
		}

		err := p.connectToServer(deviceID)
		if err != nil {
			log.Printf("Disconnected: %v. Retrying in 5 seconds...", err)
			
			// Wait 5 seconds before retrying, but allow immediate exit if service is stopped[cite: 2]
			select {
			case <-time.After(5 * time.Second):
			case <-p.exit:
				return
			}
		}
	}
}

func (p *program) connectToServer(deviceID string) error {
	log.Printf("Connecting to Yamux control plane at %s...", controlAddress)
	
	// 1. Dial Raw TCP
	conn, err := net.Dial("tcp", controlAddress)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 2. Wrap in Yamux Client
	session, err := yamux.Client(conn, nil)
	if err != nil {
		return err
	}
	defer session.Close()

	// 3. Open Stream 0 for the Control Channel
	controlStream, err := session.Open()
	if err != nil {
		return err
	}
	defer controlStream.Close()

	// If the service is stopped, close the session immediately
	go func() {
		select {
		case <-p.exit:
			session.Close()
		}
	}()

	encoder := json.NewEncoder(controlStream)
	decoder := json.NewDecoder(controlStream)

	// Send HELLO
	err = sendMsg(encoder, protocol.Message{
		Type:     protocol.MsgHello,
		DeviceID: deviceID,
		Version:  agentVersion,
	})
	if err != nil {
		return err
	}

	// Start Heartbeat loop[cite: 2]
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ticker.C:
				sendMsg(encoder, protocol.Message{Type: protocol.MsgHeartbeat})
			case <-p.exit:
				return
			}
		}
	}()

	// Main Read Loop[cite: 2]
	for {
		var msg protocol.Message
		err := decoder.Decode(&msg)
		if err != nil {
			return err // Network error or connection closed; return triggers reconnect[cite: 2]
		}

		switch msg.Type {
		case protocol.MsgAuthOK:
			log.Println("Authenticated with VPS successfully.")
		case protocol.MsgExec:
			go func(req protocol.Message) {
				res := executeOperation(req)
				sendMsg(encoder, res)
			}(msg)

		// dispatcher loop for tunnels:[cite: 2]
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

// sendMsg now takes a JSON encoder instead of a WebSocket[cite: 2]
func sendMsg(enc *json.Encoder, msg protocol.Message) error {
	writeMutex.Lock()
	defer writeMutex.Unlock()
	return enc.Encode(msg)
}

// getDeviceID handles Phase 4 Persistent Identity rules[cite: 2]
func getDeviceID() string {
	var dataDir string
	if runtime.GOOS == "windows" {
		dataDir = filepath.Join(os.Getenv("ProgramData"), "Kotman")
	} else {
		dataDir = "/var/lib/kotman"
	}

	os.MkdirAll(dataDir, 0755)
	idFile := filepath.Join(dataDir, "device-id")

	// Try to read existing ID[cite: 2]
	b, err := os.ReadFile(idFile)
	if err == nil && len(b) > 0 {
		return string(b)
	}

	// Generate new ID if not found[cite: 2]
	newID := make([]byte, 16)
	rand.Read(newID)
	idStr := hex.EncodeToString(newID)

	os.WriteFile(idFile, []byte(idStr), 0644)
	return idStr
}

// Handler function[cite: 2]
func handleTunnelReq(req protocol.Message) {
	streamID := req.RequestID
	localPort, _ := strconv.Atoi(req.Args["local_port"])

	// 1. Dial Local Service[cite: 2]
	tcp, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		log.Printf("TunnelReq[%s]: failed to dial local port %d: %v", streamID, localPort, err)
		return
	}

	// 2. Dial VPS side-channel (Still using WebSocket temporarily for Phase 6A)[cite: 2]
	wsURL := fmt.Sprintf("%s?stream_id=%s", tunnelDataURL, streamID)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Printf("TunnelReq[%s]: failed to dial VPS side-channel: %v", streamID, err)
		tcp.Close()
		return
	}

	// 3. Link them![cite: 2]
	go tunnel.Pump(ws, tcp)
}