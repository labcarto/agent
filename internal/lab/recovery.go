package lab

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Recovery is the "rebuild this host from scratch" plan: what to back up, which networks to create first, and a
// compose file per project rebuilt from the snapshot. Rebuilt, not copied: env values, commands, devices and most
// labels are not recorded (secrets stay off the server), so each file says what it can't know.
type Recovery struct {
	Data     []string         `json:"data"`     // bind paths and named volumes to back up, "path" or "volume name"
	Networks []string         `json:"networks"` // shared networks to create before any project starts
	Projects []RecoverProject `json:"projects"`
}

type RecoverProject struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"` // where the original compose file lived ("" for standalone containers)
	Compose string `json:"compose"`
}

var replicaSuffix = regexp.MustCompile(`[-_]\d+$`)

// service is the compose service name: "immich-server-1" in project "immich" -> "server".
func service(project, container string) string {
	s := container
	for _, sep := range []string{"-", "_"} {
		if strings.HasPrefix(s, project+sep) {
			s = strings.TrimPrefix(s, project+sep)
			s = replicaSuffix.ReplaceAllString(s, "")
			break
		}
	}
	if s == "" {
		return container
	}
	return s
}

// q quotes a YAML scalar: a JSON string is a valid YAML double-quoted string.
func q(s string) string { b, _ := json.Marshal(s); return string(b) }

func RecoveryPlan(snap Snapshot) Recovery {
	_, users, _ := Topology(snap)
	shared := map[string]bool{}
	for n, u := range users {
		if len(u) > 1 && n != "bridge" && n != "host" && n != "none" {
			shared[n] = true
		}
	}
	data := map[string]bool{}
	projects := map[string][]string{}
	for _, n := range Sorted(snap) {
		projects[snap[n].Project] = append(projects[snap[n].Project], n)
	}
	r := Recovery{Data: []string{}, Networks: Sorted(shared), Projects: []RecoverProject{}}
	for _, proj := range Sorted(projects) {
		var L []string
		vols, nets := map[string]bool{}, map[string]bool{}
		dir := snap[projects[proj][0]].ComposeDir
		L = append(L, "# "+proj+": rebuilt by Labcarto from what was running. Compare with your own compose file if you still have it.",
			"# Not recorded, so not here: environment values, command/entrypoint, devices, user, healthcheck, most labels.",
			"services:")
		for _, n := range projects[proj] {
			c := snap[n]
			L = append(L, "  "+q(service(proj, n))+":", "    container_name: "+q(n))
			img := c.Image
			if c.RepoDigest != nil && *c.RepoDigest != "" && !strings.Contains(img, "@") {
				img += "@" + *c.RepoDigest // the exact build that ran, so a rebuild can't drift
			}
			L = append(L, "    image: "+q(img))
			if c.Restart != "" && c.Restart != "no" {
				L = append(L, "    restart: "+q(c.Restart))
			}
			if len(c.Ports) > 0 {
				L = append(L, "    ports:")
				for _, p := range c.Ports { // "8080->80/tcp" -> "8080:80"
					host, ctr, _ := strings.Cut(p, "->")
					L = append(L, "      - "+q(host+":"+strings.TrimSuffix(ctr, "/tcp")))
				}
			}
			var mounts []string
			for _, m := range c.Mounts { // "volume:pgdata -> /data", "bind:/srv/x -> /x"
				kind, rest, _ := strings.Cut(m, ":")
				src, dst, ok := strings.Cut(rest, " -> ")
				if !ok || kind != "volume" && kind != "bind" {
					continue
				}
				mounts = append(mounts, src+":"+dst)
				if kind == "volume" {
					vols[src] = true
					data[src+" (volume)"] = true
				} else if src != "/var/run/docker.sock" {
					data[src] = true
				}
			}
			if len(mounts) > 0 {
				L = append(L, "    volumes:")
				for _, m := range mounts {
					L = append(L, "      - "+q(m))
				}
			}
			var cnets []string
			for _, nw := range c.Networks {
				switch {
				case nw == "host":
					L = append(L, `    network_mode: "host"`)
				case nw == "bridge" || nw == "none" || nw == proj+"_default":
					// compose's own default network, created by "up"
				default:
					cnets = append(cnets, nw)
					nets[nw] = true
				}
			}
			if len(cnets) > 0 {
				L = append(L, "    networks:")
				for _, nw := range cnets {
					L = append(L, "      - "+q(nw))
				}
			}
			if len(c.Hosts) > 0 {
				L = append(L, "    # reachable at "+strings.Join(c.Hosts, ", ")+" through Traefik: copy its router labels from your old file")
			}
			if len(c.EnvKeys) > 0 { // names only, and the image's own defaults (PATH, …) are mixed in: a hint, not config
				L = append(L, "    # environment had: "+strings.Join(c.EnvKeys, ", "))
			}
		}
		if len(vols) > 0 {
			L = append(L, "volumes:")
			for _, v := range Sorted(vols) {
				L = append(L, "  "+q(v)+":", "    name: "+q(v)) // keep the exact name, so restored data is found
			}
		}
		if len(nets) > 0 {
			L = append(L, "networks:")
			for _, nw := range Sorted(nets) {
				if shared[nw] {
					L = append(L, "  "+q(nw)+":", "    external: true # create it first, see the steps")
				} else {
					L = append(L, "  "+q(nw)+":", "    name: "+q(nw))
				}
			}
		}
		r.Projects = append(r.Projects, RecoverProject{proj, dir, strings.Join(L, "\n") + "\n"})
	}
	r.Data = Sorted(data)
	sort.Strings(r.Networks)
	return r
}

// RecoveryMD is the plan as runbook markdown.
func RecoveryMD(r Recovery) string {
	L := []string{"## Rebuild from scratch", "", "If this host dies: install Docker on the new one, then follow these steps in order.", "",
		"1. **Restore your data.** Labcarto does not back it up; these are the paths and volumes your containers keep it in:", ""}
	if len(r.Data) == 0 {
		L = append(L, "   - _No persistent data mounted._")
	}
	for _, d := range r.Data {
		L = append(L, "   - `"+d+"`")
	}
	step := 2
	if len(r.Networks) > 0 {
		L = append(L, "", "2. **Create the shared networks.**", "", "   ```sh")
		for _, n := range r.Networks {
			L = append(L, "   docker network create "+n)
		}
		L = append(L, "   ```")
		step++
	}
	L = append(L, "", strings.Replace("N. **Start each project.** Save its compose file below as `compose.yaml` in its folder, add the environment values, then run `docker compose up -d` there.", "N", string(rune('0'+step)), 1))
	for _, p := range r.Projects {
		where := "`" + p.Dir + "`"
		if p.Dir == "" {
			where = "a folder of your choice (these ran without compose)"
		}
		L = append(L, "", "### "+p.Name, "", "Folder: "+where, "", "```yaml", strings.TrimRight(p.Compose, "\n"), "```")
	}
	return strings.Join(L, "\n")
}
