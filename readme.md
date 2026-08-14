# Kotman — Lightweight Agent & Remote Management System

Kotman is an open-source, high-performance Remote Access, Fleet Management, and Reverse Tunneling platform written in Go. It enables secure fleet administration, constrained remote command execution (RPC), and reverse port forwarding across NAT boundaries and firewalls via a single central VPS server.

Designed as a self-hosted alternative to tools like Ngrok or Tailscale, Kotman decouples the **Control Plane** (WebSockets RPC & REST API) from the **Data Plane** (side-channel byte-streaming proxies).

---

## Key Capabilities

* **Fleet Visibility & Monitoring:** Real-time online/offline heartbeats, device tracking, and node metadata inspection.
* **Controlled Remote Execution (RPC):** Dispatches predefined, whitelisted maintenance tasks to remote agents without exposing a dangerous raw shell.
* **High-Performance Reverse Tunneling:** Expose internal HTTP/TCP services running behind strict firewalls to public listening ports on your VPS.
* **Multiplexed Side-Channel Streaming:** Dedicated high-throughput WebSocket streams strictly dedicated to bi-directional binary byte pumping.
* **Native OS Daemon Integration:** Cross-platform background service management (systemd on Linux, Service Control Manager on Windows) using persistent device IDs.
* **Audited Operations:** Persistent SQLite storage tracks all registered nodes, active tunnel configurations, and RPC execution logs.

---

## System Architecture

Kotman enforces a strict separation between command orchestration and data proxying.

```text
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                  KOTMAN SYSTEM                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

 ┌──────────────────┐           Control WebSocket (Port 8080)         ┌──────────────┐
 │   Target Agent   │ <=============================================> │  VPS Server  │
 │  (Remote Machine)│                                                 └──────┬───────┘
 └────────┬─────────┘           Data Stream WebSocket (/api/tunnel/data)     │
          │                   <=======================================>      │
          │                                                                  │
 ┌────────┴─────────┐                                                 ┌──────┴───────┐
 │ Local Service    │                                                 │ SQLite DB    │
 │ (e.g. Port 3000) │                                                 │ (kotman.db)  │
 └──────────────────┘                                                 └──────┬───────┘
                                                                             │
                                                                 Admin API (Port 8081)
                                                                             │
                                                                      ┌──────┴───────┐
                                                                      │  Kotman CLI  │
                                                                      └──────────────┘

```

### Component Breakdown

1. **Central Server (`server/`)**
* **Agent Listener (`:8080`)**: Maintains persistent WebSocket connections for heartbeats and RPC control messages.
* **Admin REST API (`127.0.0.1:8081`)**: Local-only API handling CLI requests.
* **Dynamic Tunnel Manager**: Binds public TCP ports on the VPS and handles bi-directional byte-pumping between public callers and agent data streams.


2. **Client Agent (`agent/`)**
* Runs as an OS service with auto-restart resilience.
* Maintains persistent machine identity saved to disk.
* Listens for control frame instructions and initiates side-channel connections for active data streams.


3. **Admin CLI (`cli/`)**
* Lightweight binary used by administrators on the server to manage nodes, invoke tasks, and manage public proxies.



---

## Database Schema Overview

Kotman uses SQLite (`kotman.db`) for lightweight, zero-dependency persistence.

```text
  ┌──────────────────┐          ┌──────────────────┐
  │     devices      │          │     tunnels      │
  ├──────────────────┤          ├──────────────────┤
  │ device_id   (PK) │ ───────< │ tunnel_id   (PK) │
  │ nickname         │          │ device_id   (FK) │
  │ status           │          │ vps_port         │
  │ first_seen       │          │ local_port       │
  │ last_seen        │          └──────────────────┘
  │ agent_version    │
  └──────────────────┘
           │
           │                    ┌──────────────────┐
           │                    │    audit_logs    │
           │                    ├──────────────────┤
           └──────────────────< │ log_id      (PK) │
                                │ device_id   (FK) │
                                │ operation        │
                                │ status           │
                                │ timestamp        │
                                └──────────────────┘

```

---

## Directory Structure

```text
kotman/
├── agent/             # Client service, daemon lifecycle, execution dispatcher
├── cli/               # Administrative command-line utility
├── internal/
│   └── db/            # SQLite initialization, schema migrations, and query interfaces
├── protocol/          # Shared JSON message structures and control frame protocol
└── server/            # VPS Hub, HTTP/WS endpoints, proxy listener manager

```

---

## Prerequisites

* **Go Compiler:** Version 1.20 or newer
* **C Compiler (GCC / MinGW):** Required for SQLite driver compilation (`cgo`)
* **OS Support:** Linux (systemd), Windows (SCM), macOS

---

## Installation & Compilation

Clone the repository and build the executables into a `bin/` directory:

```bash
git clone https://github.com/your-repo/kotman.git
cd kotman

# Compile server binary
go build -o bin/kotman-server server/main.go

# Compile agent binary
go build -o bin/kotman-agent agent/main.go

# Compile administrative CLI binary
go build -o bin/kotman cli/main.go

```

---

## Getting Started

### 1. Launching the Central Server

Run the server binary on your central VPS:

```bash
./bin/kotman-server

```

> **Default Bindings:**
> * Agent Control WebSocket: `0.0.0.0:8080`
> * Admin REST API: `127.0.0.1:8081` *(Restricted to local loopback)*
> 
> 

---

### 2. Registering and Running the Agent

#### Persistent Identity Storage

On its initial run, the agent generates a unique, immutable 16-byte hex identity stored at:

* **Linux:** `/var/lib/kotman/device-id`
* **Windows:** `C:\ProgramData\Kotman\device-id`

#### Deploying as an OS Service

Kotman utilizes an embedded service manager (`kardianos/service`) for native daemon execution.

##### **Linux (systemd)**

```bash
# Install and register systemd unit (requires root)
sudo ./bin/kotman-agent install

# Start the background service
sudo ./bin/kotman-agent start

# Monitor service status
systemctl status KotmanAgent

```

##### **Windows (Service Control Manager)**

Open PowerShell as **Administrator**:

```powershell
# Install Windows Service
.\bin\kotman-agent.exe install

# Launch Windows Service
.\bin\kotman-agent.exe start

# Inspect status
Get-Service KotmanAgent

```

---

## CLI Command Reference

All administrative operations are executed via the `kotman` CLI on the VPS server.

### Command Matrix

| Command | Syntax | Description |
| --- | --- | --- |
| **`ps`** | `kotman ps` | Displays all registered agent nodes, online status, and last heartbeat. |
| **`inspect`** | `kotman inspect <name>` | Fetches detailed metadata for a specific machine. |
| **`rename`** | `kotman rename <old_name> <new_name>` | Assigns a human-readable alias to a node. |
| **`exec`** | `kotman exec <name> <op> [k=v ...]` | Dispatches a whitelisted maintenance operation. |
| **`tunnel -p`** | `kotman tunnel -p <VPS_PORT>:<LOCAL_PORT> <name>` | Binds a public VPS port and routes traffic to a local port on the node. |
| **`tunnel ls`** | `kotman tunnel ls` | Lists all active port forwarding bridges. |
| **`tunnel rm`** | `kotman tunnel rm <tunnel_id>` | Terminates an active tunnel and closes the public listening port. |

---

### Operations & Output Examples

#### 1. Device Discovery (`ps`)

```bash
$ kotman ps

NAME        STATUS   LAST SEEN
PC-001      online   2s ago
PC-002      online   0s ago
Server-Dev  offline  14m ago

```

#### 2. Device Metadata (`inspect`)

```bash
$ kotman inspect PC-002

Device:   PC-002
ID:       4f8b92a110cd9e01
Status:   online
Seen:     2026-08-14T17:05:00Z
Agent:    v1.2.0

```

#### 3. Remote Execution (`exec`)

```bash
$ kotman exec PC-002 system-info

os:          linux
arch:        amd64
cpus:        8
hostname:    workstation-02

```

#### 4. Reverse Tunneling Setup (`tunnel`)

Expose a web server running locally on `PC-002` (port 3000) to port `8000` on the public VPS:

```bash
# 1. Create the tunnel
$ kotman tunnel -p 8000:3000 PC-002
Tunnel created! VPS:8000 -> PC-002:3000

# 2. Verify active tunnels
$ kotman tunnel ls
ID             PC       VPS_PORT   LOCAL_PORT
t-1786727056   PC-002   8000       3000

# 3. Terminate a tunnel
$ kotman tunnel rm t-1786727056
Tunnel t-1786727056 removed successfully.

```

---

## Technical Deep-Dive: Reverse Tunnel Protocol

When a public connection is established on a bound VPS tunnel port, Kotman processes the connection through a three-stage handshake:

```text
Public User           VPS Server               Agent Node            Local App
    │                     │                        │                     │
    ├─ TCP Connect ─────> │                        │                     │
    │  (Port 8000)        │                        │                     │
    │                     ├─ WS Control Frame ───> │                     │
    │                     │  ("open_tunnel")       │                     │
    │                     │                        ├─ Dial TCP ────────> │
    │                     │                        │  (127.0.0.1:3000)   │
    │                     │ <─ WS Data Stream ─────┤                     │
    │                     │    (/api/tunnel/data)  │                     │
    │                     │                        │                     │
    │ <═══════════════════╧════════════════════════╧═══════════════════> │
    │                   Bi-Directional Proxy Data Transfer              │

```

1. **Trigger:** A public client connects to `VPS_PORT` (e.g., `8000`).
2. **Notification:** The VPS sends a JSON control frame over the main WebSocket to the target Agent containing a temporary `stream_id`.
3. **Data Channel Dial:** The Agent connects back to `http://<VPS>/api/tunnel/data?stream_id=...` over a dedicated WebSocket, while simultaneously dialing `127.0.0.1:LOCAL_PORT` on its local machine.
4. **Byte Pumping:** The VPS bridges the incoming raw TCP socket directly with the side-channel WebSocket stream, piping data in real-time until either peer closes the connection.

---

## Security Architecture

1. **Strictly Non-RCE (No Raw Shells):** The agent rejects arbitrary shell strings (`bash`, `cmd.exe`, `powershell`). Commands must be hardcoded within the agent's explicit dispatcher function.
2. **Loopback-Restricted Management API:** The API server (`8081`) binds exclusively to local loopback (`127.0.0.1`). CLI administration requires SSH/local access to the VPS.
3. **Immutable Node Tracking:** Device IDs are cryptographically generated (128-bit entropy) on first boot and locked to host storage, preventing impersonation.
4. **Audit Logging:** Every administrative action (`exec`, `tunnel create`, `rename`) generates an entry in the SQLite `audit_logs` table containing target parameters and execution status.

---

## License

Distributed under the MIT License. See `LICENSE` for more information.