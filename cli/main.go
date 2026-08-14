package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

type TunnelData struct {
	ID        string `json:"id"`
	PC        string `json:"pc"`
	VPSPort   int    `json:"vps_port"`
	LocalPort int    `json:"local_port"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: kotman <ps|rename|inspect|exec|tunnel>")
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
			runTunnelLs()
		} else if len(os.Args) >= 4 && os.Args[2] == "rm" {
			runTunnelRm(os.Args[3])
		} else if len(os.Args) == 5 && os.Args[2] == "-p" {
			parts := strings.Split(os.Args[3], ":")
			if len(parts) != 2 {
				fmt.Println("Invalid port mapping. Use VPS_PORT:LOCAL_PORT")
				return
			}
			vp, _ := strconv.Atoi(parts[0])
			lp, _ := strconv.Atoi(parts[1])
			runTunnelCreate(os.Args[4], vp, lp)
		} else {
			fmt.Println("Usage:")
			fmt.Println("  kotman tunnel ls")
			fmt.Println("  kotman tunnel rm <tunnel_id>")
			fmt.Println("  kotman tunnel -p <VPS_PORT>:<LOCAL_PORT> <pc_name>")
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

func runTunnelCreate(target string, vpsPort, localPort int) {
	payload, _ := json.Marshal(map[string]any{
		"target":     target,
		"vps_port":   vpsPort,
		"local_port": localPort,
	})

	resp, err := http.Post(AdminURL+"/tunnel/create", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		fmt.Println("Network error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Failed to create tunnel: %s\n", string(body))
		return
	}

	fmt.Printf("Tunnel created! VPS:%d -> %s:%d\n", vpsPort, target, localPort)
}

func runTunnelLs() {
	resp, err := http.Get(AdminURL + "/tunnel/ls")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()

	var tunnels []TunnelData
	json.NewDecoder(resp.Body).Decode(&tunnels)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tPC\tVPS_PORT\tLOCAL_PORT")

	for _, t := range tunnels {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\n", t.ID, t.PC, t.VPSPort, t.LocalPort)
	}
	w.Flush()
}

func runTunnelRm(tunnelID string) {
	payload, _ := json.Marshal(map[string]string{"tunnel_id": tunnelID})
	resp, err := http.Post(AdminURL+"/tunnel/rm", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Tunnel %s removed successfully.\n", tunnelID)
	} else {
		fmt.Println("Failed to remove tunnel.")
	}
}