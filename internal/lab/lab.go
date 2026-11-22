// Package lab is the snapshot model shared by the agent and the server: normalize docker inspect output, detect
// drift, judge health, and write the plain-language runbook. Pure functions, no I/O.
package lab

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Container is what an operator cares about in one `docker inspect` entry.
// Env VALUES are never stored (secrets), only the variable names.
type Container struct {
	Project    string   `json:"project"`
	ComposeDir string   `json:"compose_dir"`
	Image      string   `json:"image"`
	ImageID    *string  `json:"image_id,omitempty"` // same tag, new build (a re-pulled :latest) shows up as drift
	State      string   `json:"state"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	Health     string   `json:"health"`
	Restart    string   `json:"restart"`
	Ports      []string `json:"ports"`
	Networks   []string `json:"networks"`
	Mounts     []string `json:"mounts"`
	Hosts      []string `json:"hosts"`
	EnvKeys    []string `json:"env_keys"`
	// RepoDigest: the registry digest the image was pulled as, "" for a local build. Nil from older agents.
	// Not drift on its own: a new pull also changes image_id.
	RepoDigest *string `json:"repo_digest,omitempty"`
}

// Snapshot maps container name to container.
type Snapshot map[string]Container

func Sorted[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// field is one comparable attribute, in the agent's field order; ok=false when an older agent didn't send it.
type field struct {
	name string
	str  func(Container) (string, bool)
	list func(Container) []string
}

func s(f func(Container) string) func(Container) (string, bool) {
	return func(c Container) (string, bool) { return f(c), true }
}

var fields = []field{
	{name: "project", str: s(func(c Container) string { return c.Project })},
	{name: "compose_dir", str: s(func(c Container) string { return c.ComposeDir })},
	{name: "image", str: s(func(c Container) string { return c.Image })},
	{name: "image_id", str: func(c Container) (string, bool) {
		if c.ImageID == nil {
			return "", false
		}
		return *c.ImageID, true
	}},
	{name: "state", str: s(func(c Container) string { return c.State })},
	{name: "exit_code", str: func(c Container) (string, bool) {
		if c.ExitCode == nil {
			return "", false
		}
		return fmt.Sprint(*c.ExitCode), true
	}},
	{name: "health", str: s(func(c Container) string { return c.Health })},
	{name: "restart", str: s(func(c Container) string { return c.Restart })},
	{name: "ports", list: func(c Container) []string { return c.Ports }},
	{name: "networks", list: func(c Container) []string { return c.Networks }},
	{name: "mounts", list: func(c Container) []string { return c.Mounts }},
	{name: "hosts", list: func(c Container) []string { return c.Hosts }},
	{name: "env_keys", list: func(c Container) []string { return c.EnvKeys }},
}

func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, x := range a {
		if !in[x] && !seen[x] {
			out = append(out, x)
			seen[x] = true
		}
	}
	sort.Strings(out)
	return out
}

func orEmpty(v string) string {
	if v == "" {
		return "∅"
	}
	return v
}

// Diff returns human-readable drift lines between two snapshots.
func Diff(old, new Snapshot) []string {
	out := []string{}
	for _, n := range Sorted(new) {
		if _, ok := old[n]; !ok {
			out = append(out, fmt.Sprintf("+ %s added (%s)", n, new[n].Image))
		}
	}
	for _, n := range Sorted(old) {
		if _, ok := new[n]; !ok {
			out = append(out, fmt.Sprintf("- %s removed", n))
		}
	}
	for _, n := range Sorted(new) {
		o, ok := old[n]
		if !ok {
			continue
		}
		c := new[n]
		for _, f := range fields {
			if f.list != nil {
				added, gone := minus(f.list(c), f.list(o)), minus(f.list(o), f.list(c))
				if len(added)+len(gone) == 0 {
					continue
				}
				parts := []string{}
				for _, x := range added {
					parts = append(parts, "+"+x)
				}
				for _, x := range gone {
					parts = append(parts, "-"+x)
				}
				out = append(out, fmt.Sprintf("~ %s %s: %s", n, f.name, strings.Join(parts, ", ")))
				continue
			}
			a, hadA := f.str(o)
			b, hasB := f.str(c)
			if !hadA || !hasB || a == b { // a field the old agent didn't send is not drift
				continue
			}
			out = append(out, fmt.Sprintf("~ %s %s: %s -> %s", n, f.name, orEmpty(a), orEmpty(b)))
		}
	}
	return out
}

// Status is the docker state, except a one-shot (no restart policy) that exited with code 0 is "completed", not down.
func Status(c Container) string {
	if c.State == "exited" && c.ExitCode != nil && *c.ExitCode == 0 && (c.Restart == "" || c.Restart == "no") {
		return "completed"
	}
	return c.State
}

func Trouble(c Container) bool {
	st := Status(c)
	return (st != "running" && st != "completed") || c.Health == "unhealthy"
}

var alarm = regexp.MustCompile(`^~ (\S+) (state|health): .* -> (.*)$`)

// Alarming returns the drift lines worth an alert: a container stopped, turned unhealthy, or disappeared.
// With the new snapshot, a one-shot that finished cleanly is not an alarm.
func Alarming(lines []string, snap Snapshot) []string {
	out := []string{}
	for _, l := range lines {
		m := alarm.FindStringSubmatch(l)
		var hit bool
		if c, ok := snap[safeIdx(m, 1)]; m != nil && m[2] == "state" && snap != nil && ok {
			hit = Trouble(c) && Status(c) != "running"
		} else if m != nil {
			hit = m[3] != "running"
			if m[2] == "health" {
				hit = m[3] == "unhealthy"
			}
		}
		if hit || strings.HasPrefix(l, "- ") && strings.HasSuffix(l, " removed") {
			out = append(out, l)
		}
	}
	return out
}

func safeIdx(m []string, i int) string {
	if m == nil {
		return ""
	}
	return m[i]
}

// Finding is setup worth a second look, with the containers it applies to.
type Finding struct {
	Text       string   `json:"text"`
	Containers []string `json:"containers"`
}

var secretKey = regexp.MustCompile(`(?i)(PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|PRIVATE_KEY)`)

// Checks reads only the snapshot. Findings keep the order in which they first appear.
func Checks(snap Snapshot) []Finding {
	var out []Finding
	add := func(text, n string) {
		for i := range out {
			if out[i].Text == text {
				out[i].Containers = append(out[i].Containers, n)
				return
			}
		}
		out = append(out, Finding{text, []string{n}})
	}
	for _, n := range Sorted(snap) {
		c := snap[n]
		for _, m := range c.Mounts {
			if strings.Contains(m, "docker.sock") {
				add("Mounts the Docker socket, which gives it root on the host", n)
				break
			}
		}
		if len(c.Ports) > 0 && len(c.Hosts) > 0 {
			add("Publishes a port and is also behind Traefik, so that port bypasses the proxy", n)
		}
		if (c.Restart == "" || c.Restart == "no") && Status(c) != "completed" {
			add("Has no restart policy, so it stays down after a crash or reboot", n)
		}
		for _, k := range c.EnvKeys {
			if secretKey.MatchString(k) && !strings.HasSuffix(k, "_FILE") {
				add("Gets a secret as a plain environment variable, readable by anyone who can run docker inspect", n)
				break
			}
		}
		if c.State == "running" && c.Health == "" {
			add("Has no healthcheck, so a hang looks like running and no alert fires", n)
		}
		last := c.Image[strings.LastIndex(c.Image, "/")+1:]
		if !strings.Contains(c.Image, "@") && (!strings.Contains(last, ":") || strings.HasSuffix(c.Image, ":latest")) {
			add("Runs an unpinned :latest image, so a pull can change it silently", n)
		}
	}
	return out
}

// Attention lists the containers that are down or unhealthy (a one-shot that finished cleanly is fine).
func Attention(snap Snapshot) []string {
	out := []string{}
	for _, n := range Sorted(snap) {
		if Trouble(snap[n]) {
			out = append(out, n)
		}
	}
	return out
}

// StateText is "exited/unhealthy" style: the raw state plus health when there is one.
func StateText(c Container) string {
	if c.Health != "" {
		return c.State + "/" + c.Health
	}
	return c.State
}

func AttentionLines(snap Snapshot) []string {
	out := []string{}
	for _, n := range Attention(snap) {
		out = append(out, fmt.Sprintf("%s (%s)", n, StateText(snap[n])))
	}
	return out
}

// KindOf classifies a drift line for the history filter: add, del or chg.
func KindOf(line string) string {
	switch {
	case strings.HasPrefix(line, "+"):
		return "add"
	case strings.HasPrefix(line, "-"):
		return "del"
	}
	return "chg"
}

func Running(snap Snapshot) int {
	n := 0
	for _, c := range snap {
		if c.State == "running" {
			n++
		}
	}
	return n
}

// Topology: networks per project, projects per network, and hubs = networks shared by >1 project (biggest first).
func Topology(snap Snapshot) (netsOf map[string]map[string]bool, users map[string]map[string]bool, hubs []string) {
	netsOf, users = map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, c := range snap {
		if netsOf[c.Project] == nil {
			netsOf[c.Project] = map[string]bool{}
		}
		for _, n := range c.Networks {
			netsOf[c.Project][n] = true
		}
	}
	for p, nets := range netsOf {
		for n := range nets {
			if users[n] == nil {
				users[n] = map[string]bool{}
			}
			users[n][p] = true
		}
	}
	for n, u := range users {
		if len(u) > 1 && n != "bridge" && n != "host" {
			hubs = append(hubs, n)
		}
	}
	sort.Slice(hubs, func(i, j int) bool {
		if len(users[hubs[i]]) != len(users[hubs[j]]) {
			return len(users[hubs[i]]) > len(users[hubs[j]])
		}
		return hubs[i] < hubs[j]
	})
	return
}

func node(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func code(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return strings.Join(out, ", ")
}

// Runbook renders the snapshot as Markdown. now is the display time of the snapshot.
func Runbook(snap Snapshot, changesMD, host, now string, mermaid, recovery bool) string {
	projects := map[string]Snapshot{}
	for name, c := range snap {
		if projects[c.Project] == nil {
			projects[c.Project] = Snapshot{}
		}
		projects[c.Project][name] = c
	}
	// networks used by >1 project = the real dependencies (e.g. a shared `db` network)
	_, users, _ := Topology(snap)
	shared := map[string][]string{}
	for n, u := range users {
		if len(u) > 1 && n != "bridge" && n != "host" {
			shared[n] = Sorted(u)
		}
	}
	var bad []string
	for _, n := range Attention(snap) {
		c := snap[n]
		bad = append(bad, fmt.Sprintf("`%s` (%s)", n, StateText(c)))
	}

	L := []string{"# Runbook: " + host, "_Generated " + now + " by Labcarto. Do not edit; regenerated on every snapshot._", "",
		fmt.Sprintf("**%d containers** in **%d projects**, %d running.", len(snap), len(projects), Running(snap))}
	if len(bad) > 0 {
		L = append(L, "", "**Needs attention:** "+strings.Join(bad, ", "))
	}
	if found := Checks(snap); len(found) > 0 {
		L = append(L, "", "## Worth a look", "")
		for _, f := range found {
			L = append(L, "- "+f.Text+": "+code(f.Containers))
		}
	}
	if len(shared) > 0 {
		if mermaid {
			L = append(L, "", "## Map", "", "```mermaid", "graph LR")
			for _, net := range Sorted(shared) {
				L = append(L, fmt.Sprintf("  net_%s((%s))", node(net), net))
				for _, p := range shared[net] {
					L = append(L, fmt.Sprintf("  %s[%s] --- net_%s", node(p), p, node(net)))
				}
			}
			L = append(L, "```")
		}
		L = append(L, "", "## Shared networks", "")
		for _, net := range Sorted(shared) {
			L = append(L, fmt.Sprintf("- **%s** links %d projects: %s", net, len(shared[net]), strings.Join(shared[net], ", ")))
		}
	}
	L = append(L, "", "## Projects")
	for _, proj := range Sorted(projects) {
		cs := projects[proj]
		names := Sorted(cs)
		first := cs[names[0]]
		L = append(L, "", "### "+proj)
		if first.ComposeDir != "" {
			L = append(L, fmt.Sprintf("Compose dir: `%s`. Restart: `cd %s && docker compose up -d`", first.ComposeDir, first.ComposeDir))
		}
		hosts := map[string]bool{}
		for _, c := range cs {
			for _, h := range c.Hosts {
				hosts[h] = true
			}
		}
		if len(hosts) > 0 {
			L = append(L, "Reachable at: "+code(Sorted(hosts)))
		}
		L = append(L, "", "| Container | Image | State | Ports | Data |", "|---|---|---|---|---|")
		for _, n := range names {
			c := cs[n]
			state := c.State
			if Status(c) == "completed" {
				state = "completed (exit 0)"
			}
			if c.Health != "" {
				state += " (" + c.Health + ")"
			}
			var data []string
			for _, m := range c.Mounts {
				if !strings.HasPrefix(m, "bind:/var/run/docker.sock") {
					data = append(data, m)
				}
			}
			ports := strings.Join(c.Ports, ", ")
			if ports == "" {
				ports = "-"
			}
			d := strings.Join(data, "<br>")
			if d == "" {
				d = "none"
			}
			L = append(L, fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |", n, c.Image, state, ports, d))
		}
	}
	if changesMD == "" {
		changesMD = "_No drift recorded yet._"
	}
	if recovery { // subscription feature: left out of read-only accounts' exports
		L = append(L, "", RecoveryMD(RecoveryPlan(snap)))
	}
	L = append(L, "", "## Recent changes", "", changesMD, "")
	return strings.Join(L, "\n")
}
