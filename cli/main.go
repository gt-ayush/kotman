package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"kotman/protocol"
)

const AdminURL = "http://127.0.0.1:8081/api"

type DeviceData struct {
	DeviceID     string `json:"device_id"`
	Nickname     string `json:"nickname"`
	Status       string `json:"status"`
	FirstSeen    string `json:"first_seen"`
	LastSeen     string `json:"last_seen"`
	AgentVersion string `json:"agent_version"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: kotman <ps|rename|inspect|exec>")
		return
	}

	cmd := os.Args[1]
	switch cmd {
	case "ps":
		runPS()
	case "rename":
		if len(os.Args) != 4 {
			fmt.Println("Usage: kotman rename <old_name> <new_name>")
			return
		}
		runRename(os.Args[2], os.Args[3])
	case "inspect":
		if len(os.Args) != 3 {
			fmt.Println("Usage: kotman inspect <name>")
			return
		}
		runInspect(os.Args[2])
	case "exec":
		if len(os.Args) < 4 {
			fmt.Println("Usage: kotman exec <pc_name> <operation> [key=value ...]")
			return
		}

		args := make(map[string]string)
		for _, arg := range os.Args[4:] {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				args[parts[0]] = parts[1]
			}
		}
		runExec(os.Args[2], os.Args[3], args)
	case "tunnel":
		if len(os.Args) >= 3 && os.Args[2] == "ls" {
			// call /api/tunnel/ls
		} else if len(os.Args) >= 4 && os.Args[2] == "rm" {
			// call /api/tunnel/rm
		} else if len(os.Args) == 5 && os.Args[2] == "-p" {
			// e.g., kotman tunnel -p 8000:3000 gaming-pc
			parts := strings.Split(os.Args[3], ":")
			vpsPort, _ := strconv.Atoi(parts[0])
			localPort, _ := strconv.Atoi(parts[1])
			targetPC := os.Args[4]
			// call /api/tunnel/create
		}
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
	}
}

func runPS() {
	resp, err := http.Get(AdminURL + "/ps")
	if err != nil {
		fmt.Println("Error connecting to VPS:", err)
		return
	}
	defer resp.Body.Close()

	var devices []DeviceData
	json.NewDecoder(resp.Body).Decode(&devices)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tLAST SEEN")

	for _, d := range devices {
		t, _ := time.Parse(time.RFC3339, d.LastSeen)
		lastSeenMsg := time.Since(t).Round(time.Second).String() + " ago"
		fmt.Fprintf(w, "%s\t%s\t%s\n", d.Nickname, d.Status, lastSeenMsg)
	}
	w.Flush()
}

func runRename(oldName, newName string) {
	payload, _ := json.Marshal(map[string]string{
		"old_name": oldName,
		"new_name": newName,
	})

	resp, err := http.Post(AdminURL+"/rename", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Renamed %s to %s\n", oldName, newName)
	} else {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Failed to rename: %s\n", string(body))
	}
}

func runInspect(name string) {
	resp, err := http.Get(AdminURL + "/inspect?name=" + name)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Device not found or error occurred.")
		return
	}

	var d DeviceData
	json.NewDecoder(resp.Body).Decode(&d)

	fmt.Printf("Device:\t %s\n", d.Nickname)
	fmt.Printf("ID:\t %s\n", d.DeviceID)
	fmt.Printf("Status:\t %s\n", d.Status)
	fmt.Printf("Seen:\t %s\n", d.LastSeen)
	fmt.Printf("Agent:\t %s\n", d.AgentVersion)
}

func runExec(target, operation string, args map[string]string) {
	payload, _ := json.Marshal(map[string]any{
		"target":    target,
		"operation": operation,
		"args":      args,
	})

	resp, err := http.Post(AdminURL+"/exec", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		fmt.Println("Network error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGatewayTimeout {
		fmt.Println("Error: Operation timed out (device may have disconnected).")
		return
	}

	var result protocol.Message
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Success {
		for k, v := range result.Result {
			fmt.Printf("%s:\t%s\n", k, v)
		}
	} else {
		fmt.Printf("Operation Failed: %s\n", result.Error)
	}
}
