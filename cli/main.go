package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"
)

const AdminURL = "http://127.0.0.1:8081/api"

type DeviceData struct {
	DeviceID     string
	Nickname     string
	Status       string
	FirstSeen    string
	LastSeen     string
	AgentVersion string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: kotman <ps|rename|inspect>")
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
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
	}
}

func runPS() {
	resp, _ := http.Get(AdminURL + "/ps")
	var devices []DeviceData
	json.NewDecoder(resp.Body).Decode(&devices)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tLAST SEEN")
	
	for _, d := range devices {
		// Calculate time ago
		t, _ := time.Parse(time.RFC3339, d.LastSeen)
		lastSeenMsg := time.Since(t).Round(time.Second).String() + " ago"
		fmt.Fprintf(w, "%s\t%s\t%s\n", d.Nickname, d.Status, lastSeenMsg)
	}
	w.Flush()
}

// runRename and runInspect implementations make POST/GET requests 
// to their respective endpoints and print success/JSON output.
