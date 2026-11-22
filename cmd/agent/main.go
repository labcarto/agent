// Command agent snapshots docker state -> detects drift -> writes a plain-language runbook.
//
//	labcarto [--data DIR]          snapshot, log changes, write DIR/runbook.md (local mode)
//	labcarto --push URL --token T  send the snapshot to a Labcarto server instead
//	labcarto --dry-run             print the exact JSON --push would send, send nothing
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"labcarto/internal/lab"
)

func docker(args ...string) ([]byte, error) {
	out, err := exec.Command("docker", args...).Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return nil, fmt.Errorf("docker %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

func snapshot() (lab.Snapshot, error) {
	ids, err := docker("ps", "-aq")
	if err != nil {
		return nil, err
	}
	snap := lab.Snapshot{}
	if len(strings.Fields(string(ids))) == 0 {
		return snap, nil
	}
	raw, err := docker(append([]string{"inspect"}, strings.Fields(string(ids))...)...)
	if err != nil {
		return nil, err
	}
	var all []lab.Inspect
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	digests := repoDigests(all)
	for _, c := range all {
		ct := lab.Normalize(c)
		d := lab.RepoDigest(c.Config.Image, digests[c.Image])
		ct.RepoDigest = &d // always set, even "", so the server can tell this agent from an older one
		snap[strings.TrimPrefix(c.Name, "/")] = ct
	}
	return snap, nil
}

// repoDigests maps image id -> RepoDigests, for the server's image update check.
func repoDigests(all []lab.Inspect) map[string][]string {
	ids, seen := []string{}, map[string]bool{}
	for _, c := range all {
		if !seen[c.Image] {
			seen[c.Image] = true
			ids = append(ids, c.Image)
		}
	}
	// stdout holds every image found even when one id is gone (exit 1), so the error alone is not a reason to give up
	out, _ := exec.Command("docker", append([]string{"image", "inspect"}, ids...)...).Output()
	var imgs []struct {
		ID          string `json:"Id"`
		RepoDigests []string
	}
	json.Unmarshal(out, &imgs)
	m := map[string][]string{}
	for _, i := range imgs {
		m[i.ID] = i.RepoDigests
	}
	return m
}

func main() {
	exe, _ := os.Executable()
	data := flag.String("data", filepath.Join(filepath.Dir(exe), "labcarto-data"), "local mode: where snapshots and the runbook go")
	pushURL := flag.String("push", "", "Labcarto server URL")
	tok := flag.String("token", "", "Labcarto token (with --push)")
	dry := flag.Bool("dry-run", false, "print the exact JSON --push would send, send nothing")
	flag.Parse()
	var err error
	if *dry {
		err = dryRun()
	} else if *pushURL != "" {
		err = push(*pushURL, *tok)
	} else {
		err = local(*data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// payload is the exact body --push sends; --dry-run prints it.
func payload(snap lab.Snapshot) []byte {
	host, _ := os.Hostname()
	b, _ := json.Marshal(map[string]any{"host": host, "containers": snap})
	return b
}

func dryRun() error {
	snap, err := snapshot()
	if err != nil {
		return err
	}
	var out bytes.Buffer
	json.Indent(&out, payload(snap), "", "  ")
	fmt.Println(out.String())
	return nil
}

func push(url, tok string) error {
	snap, err := snapshot()
	if err != nil {
		return err
	}
	body := payload(snap)
	req, err := http.NewRequest("POST", strings.TrimRight(url, "/")+"/api/snapshot", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("push refused (%d): %s", resp.StatusCode, msg)
	}
	fmt.Println(string(msg))
	return nil
}

func local(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	latestPath, logPath := filepath.Join(dir, "latest.json"), filepath.Join(dir, "changes.md")
	snap, err := snapshot()
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(latestPath); err == nil {
		var old lab.Snapshot
		if err := json.Unmarshal(b, &old); err != nil {
			return err
		}
		if changes := lab.Diff(old, snap); len(changes) > 0 {
			var entry strings.Builder
			entry.WriteString("**" + time.Now().Format("2006-01-02 15:04") + "**\n")
			for _, c := range changes {
				entry.WriteString("- `" + c + "`\n")
			}
			f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			f.WriteString(entry.String() + "\n")
			f.Close()
			fmt.Printf("%d change(s):\n  %s\n", len(changes), strings.Join(changes, "\n  "))
		} else {
			fmt.Println("no drift")
		}
	} else {
		fmt.Printf("first snapshot: %d containers\n", len(snap))
	}
	b, _ := json.MarshalIndent(snap, "", " ")
	if err := os.WriteFile(latestPath, b, 0o644); err != nil {
		return err
	}
	// ponytail: whole change log embedded in runbook; keep last N entries once it grows
	recent, _ := os.ReadFile(logPath)
	host, _ := os.Hostname()
	out := filepath.Join(dir, "runbook.md")
	if err := os.WriteFile(out, []byte(lab.Runbook(snap, string(recent), host, time.Now().Format("2006-01-02 15:04"), true, true)), 0o644); err != nil {
		return err
	}
	fmt.Println("runbook:", out)
	return nil
}
