# Kotman - Lightweight Agent & Remote Management System

Kotman is a lightweight, cross-platform remote node management system built in Go. It connects remote agent devices to a central VPS server via WebSocket, providing secure remote inspection, persistent device identification, and explicit, shell-free command execution through a local CLI.

---

## Architecture

```text
┌──────────────┐         WebSocket (Port 8080)        ┌──────────────┐
│ Kotman Agent │ <==================================> │ Kotman Server│
└──────────────┘                                      └──────┬───────┘
  (Linux / Win)                                              │ SQLite DB
                                                      Local HTTP API (Port 8081)
                                                             │
                                                      ┌──────┴───────┐
                                                      │  Kotman CLI  │
                                                      └──────────────┘

```

* **Server:** Manages WebSocket connections, tracks node health, logs audit records, and exposes an HTTP REST API on `localhost:8081` for administrative tasks.
* **Agent:** Runs as a native background OS service (systemd or Windows Service), maintains persistent device identity, auto-reconnects on network loss, and handles pre-authorized operations.
* **CLI:** Command-line tool used by administrators on the server to query nodes and issue remote execution tasks.

---

## Directory Structure

```text
kotman/
├── agent/          # Background client agent and service wrapper
├── cli/            # Administrative command-line tool
├── internal/
│   └── db/         # SQLite database initialization, registration, and audit logs
├── protocol/       # Shared JSON message protocol definitions
└── server/         # Central VPS server and local admin API

```

---

## Prerequisites

* **Go**: 1.20 or newer
* **Database**: SQLite3 driver dependencies (handled automatically via `cgo` / `go.sqlite3`)

---

## Getting Started

### 1. Build the Binaries

```bash
# Build the Server
go build -o bin/kotman-server server/main.go

# Build the Agent
go build -o bin/kotman-agent agent/main.go

# Build the CLI
go build -o bin/kotman cli/main.go

```

### 2. Start the Server

```bash
./bin/kotman-server

```

* The server creates `kotman.db` automatically on first run.
* Agent listener: `0.0.0.0:8080`
* CLI API listener: `127.0.0.1:8081`

---

## OS Service Setup (Agent)

The agent uses `kardianos/service` to run seamlessly as an OS-managed daemon with automatic startup on boot and auto-recovery on failure.

### Persistent Identity Locations

The agent generates a 16-byte hex device ID once and reuses it indefinitely:

* **Linux:** `/var/lib/kotman/device-id`
* **Windows:** `C:\ProgramData\Kotman\device-id`

### Linux (systemd)

```bash
# Install and start the service (requires elevated privileges)
sudo ./bin/kotman-agent install
sudo ./bin/kotman-agent start

# Check service status
systemctl status KotmanAgent

```

### Windows (Service Control Manager)

Open PowerShell as **Administrator**:

```powershell
# Install and start the service
.\bin\kotman-agent.exe install
.\bin\kotman-agent.exe start

# Check service status
Get-Service KotmanAgent

```

To stop or remove the service on either OS, use `stop` or `uninstall`:

```bash
sudo ./bin/kotman-agent stop
sudo ./bin/kotman-agent uninstall

```

---

## CLI Reference

All CLI commands interact with the local admin API (`[http://127.0.0.1:8081/api](http://127.0.0.1:8081/api)`).

| Command | Usage | Description |
| --- | --- | --- |
| **`ps`** | `kotman ps` | Lists all registered nodes, online status, and last seen timestamps. |
| **`inspect`** | `kotman inspect <nickname>` | Shows detailed registration info and agent version for a node. |
| **`rename`** | `kotman rename <old_name> <new_name>` | Assigns a custom alias to a node. |
| **`exec`** | `kotman exec <nickname> <op> [k=v]` | Dispatches an explicit command to an online node. |

---

## Remote Execution Commands

To prevent arbitrary Remote Code Execution (RCE), Kotman uses explicit dispatch logic instead of raw shell execution.

### Examples

```bash
# Get agent status
./bin/kotman exec Main status

# Retrieve host OS, architecture, and CPU count
./bin/kotman exec Main system-info

# Run a whitelisted task with arguments
./bin/kotman exec Main run-task task=update-dashboard

```

### Response Codes & Errors

* **`operation not permitted`**: The command is not implemented in the agent's dispatcher.
* **`Device offline or not found`**: Target node is not connected to the WebSocket server.
* **`timeout`**: Agent did not respond within the 10-second request window.

---

## Security Model

1. **Zero Shell Execution:** The agent does not execute raw shell commands (`bash`, `cmd`, `powershell`). Operations must be explicitly coded in the agent's `executeOperation` function.
2. **Local-Only Admin API:** The HTTP API handling CLI requests listens exclusively on `127.0.0.1:8081`.
3. **Audit Logging:** Every execution request (successful or failed) is stored in the `audit_logs` SQLite table on the VPS with timestamps and operation details.