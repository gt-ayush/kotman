package tunnel

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Manager tracks active VPS listeners and handles incoming data channels
type Manager struct {
	listeners map[string]net.Listener          // key: tunnelID
	streams   map[string]chan *websocket.Conn  // key: streamID (for linking WS to TCP)
	mu        sync.Mutex
	
	// Callback to send the request down the existing control WS
	SendControlReq func(deviceID, streamID string, localPort int) error
}

func NewManager(reqFunc func(deviceID, streamID string, localPort int) error) *Manager {
	return &Manager{
		listeners:      make(map[string]net.Listener),
		streams:        make(map[string]chan *websocket.Conn),
		SendControlReq: reqFunc,
	}
}

// StartListener opens the TCP port on the VPS and waits for connections
func (m *Manager) StartListener(tunnelID, deviceID string, vpsPort, localPort int) error {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", vpsPort))
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.listeners[tunnelID] = l
	m.mu.Unlock()

	go m.acceptLoop(tunnelID, l, deviceID, localPort)
	return nil
}

func (m *Manager) StopListener(tunnelID string) {
	m.mu.Lock()
	if l, ok := m.listeners[tunnelID]; ok {
		l.Close()
		delete(m.listeners, tunnelID)
	}
	m.mu.Unlock()
}

func (m *Manager) acceptLoop(tunnelID string, l net.Listener, deviceID string, localPort int) {
	for {
		tcpConn, err := l.Accept()
		if err != nil {
			return // Listener closed
		}

		// Generate a single-use nonce for this specific TCP connection
		streamID := fmt.Sprintf("st-%d", time.Now().UnixNano())

		// Setup the channel to receive the incoming WS connection from the Agent
		ch := make(chan *websocket.Conn, 1)
		m.mu.Lock()
		m.streams[streamID] = ch
		m.mu.Unlock()

		// Tell the Agent to dial back via the control channel
		err = m.SendControlReq(deviceID, streamID, localPort)
		if err != nil {
			tcpConn.Close()
			m.cleanupStream(streamID)
			continue
		}

		// Wait up to 10 seconds for the agent to establish the data WS
		go func(conn net.Conn, sID string) {
			select {
			case wsConn := <-ch:
				Pump(wsConn, conn) // Shuffle bytes!
			case <-time.After(10 * time.Second):
				conn.Close()
			}
			m.cleanupStream(sID)
		}(tcpConn, streamID)
	}
}

// RegisterDataStream links an incoming Agent WebSocket to a waiting VPS TCP connection
func (m *Manager) RegisterDataStream(streamID string, ws *websocket.Conn) bool {
	m.mu.Lock()
	ch, ok := m.streams[streamID]
	m.mu.Unlock()

	if ok {
		ch <- ws
		return true
	}
	return false
}

func (m *Manager) cleanupStream(streamID string) {
	m.mu.Lock()
	delete(m.streams, streamID)
	m.mu.Unlock()
}

// Pump shuffles bytes bidirectionally between a WebSocket and a TCP connection
func Pump(ws *websocket.Conn, tcp net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// WS -> TCP
	go func() {
		defer wg.Done()
		defer tcp.Close()
		defer ws.Close()
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil || mt != websocket.BinaryMessage {
				break
			}
			if _, err := tcp.Write(data); err != nil {
				break
			}
		}
	}()

	// TCP -> WS
	go func() {
		defer wg.Done()
		defer tcp.Close()
		defer ws.Close()
		buf := make([]byte, 32768)
		for {
			n, err := tcp.Read(buf)
			if n > 0 {
				if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	wg.Wait()
}