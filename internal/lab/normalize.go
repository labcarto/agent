package lab

import (
	"regexp"
	"sort"
	"strings"
)

// Inspect is the part of one `docker inspect` entry the agent reads.
type Inspect struct {
	Name   string
	Image  string
	Config struct {
		Image  string
		Labels map[string]string
		Env    []string
	}
	State struct {
		Status   string
		ExitCode int
		Health   *struct{ Status string }
	}
	HostConfig struct {
		RestartPolicy struct{ Name string }
	}
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIp, HostPort string }
		Networks map[string]any
	}
	Mounts []struct{ Type, Name, Source, Destination string }
}

var (
	routerRule = regexp.MustCompile(`^traefik\.http\.routers\.[^.]+\.rule$`)
	hostRule   = regexp.MustCompile("Host\\(`([^`]+)`\\)")
)

// Normalize reduces one `docker inspect` entry to a Container. Env values are dropped, only names are kept.
func Normalize(c Inspect) Container {
	labels := c.Config.Labels
	ports := []string{}
	for p, binds := range c.NetworkSettings.Ports {
		for _, b := range binds {
			if b.HostIp == "0.0.0.0" || b.HostIp == "" { // skip the duplicate IPv6 binding
				ports = append(ports, b.HostPort+"->"+p)
			}
		}
	}
	sort.Strings(ports)
	mounts := []string{}
	for _, m := range c.Mounts {
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		}
		mounts = append(mounts, m.Type+":"+src+" -> "+m.Destination)
	}
	sort.Strings(mounts)
	hosts := map[string]bool{}
	for k, v := range labels {
		if routerRule.MatchString(k) {
			for _, h := range hostRule.FindAllStringSubmatch(v, -1) {
				hosts[h[1]] = true
			}
		}
	}
	env := []string{}
	for _, e := range c.Config.Env {
		k, _, _ := strings.Cut(e, "=")
		env = append(env, k)
	}
	sort.Strings(env)
	project := labels["com.docker.compose.project"]
	if project == "" {
		project = "(standalone)"
	}
	id := c.Image[strings.LastIndex(c.Image, ":")+1:]
	if len(id) > 12 {
		id = id[:12]
	}
	health := ""
	if c.State.Health != nil {
		health = c.State.Health.Status
	}
	exit := c.State.ExitCode
	return Container{
		Project: project, ComposeDir: labels["com.docker.compose.project.working_dir"], Image: c.Config.Image, ImageID: &id,
		State: c.State.Status, ExitCode: &exit, Health: health, Restart: c.HostConfig.RestartPolicy.Name,
		Ports: ports, Networks: Sorted(c.NetworkSettings.Networks), Mounts: mounts, Hosts: Sorted(hosts), EnvKeys: env,
	}
}

// familiar: "docker.io/library/postgres" and "postgres" are the same repository.
func familiar(repo string) string {
	for _, p := range []string{"docker.io/", "index.docker.io/", "library/"} {
		repo = strings.TrimPrefix(repo, p)
	}
	return repo
}

// RepoDigest picks, from an image's RepoDigests ("postgres@sha256:…"), the digest for the repository the container
// names ("postgres:16"). "" when it was never pulled from there (a local build, a retag).
func RepoDigest(image string, repoDigests []string) string {
	repo := image
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		repo = image[:i]
	}
	repo, _, _ = strings.Cut(repo, "@")
	for _, rd := range repoDigests {
		if r, d, ok := strings.Cut(rd, "@"); ok && familiar(r) == familiar(repo) {
			return d
		}
	}
	return ""
}
