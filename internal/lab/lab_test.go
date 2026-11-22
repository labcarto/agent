package lab

import (
	"encoding/json"
	"encoding/xml"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestDiffAndAlarms(t *testing.T) {
	base := Container{Image: "pg:16", State: "running", Ports: []string{"5432->5432/tcp"}, Networks: []string{"db"}}
	with := func(f func(*Container)) Container { c := base; f(&c); return c }
	old := Snapshot{"pg": base, "gone": base}
	new := Snapshot{"pg": with(func(c *Container) { c.Image = "pg:17"; c.Networks = []string{"db", "proxy"} }), "fresh": base}
	d := Diff(old, new)
	for _, want := range []string{"+ fresh added (pg:16)", "- gone removed", "~ pg image: pg:16 -> pg:17", "~ pg networks: +proxy"} {
		if !slices.Contains(d, want) {
			t.Fatal(want, d)
		}
	}
	if len(Diff(new, new)) != 0 {
		t.Fatal("self diff")
	}
	if got := Alarming(d, nil); !reflect.DeepEqual(got, []string{"- gone removed"}) {
		t.Fatal(got)
	}
	stop := Diff(Snapshot{"a": with(func(c *Container) { c.Health = "healthy" })},
		Snapshot{"a": with(func(c *Container) { c.State = "exited"; c.Health = "unhealthy" })})
	if len(stop) != 2 || !reflect.DeepEqual(Alarming(stop, nil), stop) {
		t.Fatal(stop)
	}
	calm := Diff(Snapshot{"a": with(func(c *Container) { c.State = "exited"; c.Health = "starting" })},
		Snapshot{"a": with(func(c *Container) { c.Health = "healthy" })})
	if len(Alarming(calm, nil)) != 0 { // recoveries never alert
		t.Fatal(calm)
	}
	// one-shots: exit 0 with no restart policy is done, not down; a non-zero exit still alarms
	job := with(func(c *Container) { c.Restart = "no"; c.ExitCode = ptr(0) })
	ended := with(func(c *Container) { c.Restart = "no"; c.ExitCode = ptr(0); c.State = "exited" })
	done := Diff(Snapshot{"m": job}, Snapshot{"m": ended})
	if len(done) == 0 || len(Alarming(done, Snapshot{"m": ended})) != 0 {
		t.Fatal(done)
	}
	failed := ended
	failed.ExitCode = ptr(1)
	if !reflect.DeepEqual(Alarming(done, Snapshot{"m": failed}), done) || !reflect.DeepEqual(Alarming(done, nil), done) {
		t.Fatal("non-zero exit or history filter must alarm")
	}
	// a field an older agent didn't send is not drift
	if len(Diff(Snapshot{"a": base}, Snapshot{"a": with(func(c *Container) { c.ImageID = ptr("abc") })})) != 0 {
		t.Fatal("missing field counted as drift")
	}
}

func TestChecks(t *testing.T) {
	ck := Checks(Snapshot{
		"t": {Image: "traefik", State: "running", Restart: "always", Mounts: []string{"bind:/var/run/docker.sock -> /var/run/docker.sock"},
			Ports: []string{"80->80/tcp"}, Hosts: []string{"t.lan"}, EnvKeys: []string{"CF_API_TOKEN", "TZ"}},
		"j":  {Image: "reg:5000/job:1.2", State: "exited", Restart: "no", ExitCode: ptr(0), EnvKeys: []string{"DB_PASSWORD_FILE"}},
		"db": {Image: "postgres:16", State: "running", Restart: "always", Health: "healthy"},
	})
	if len(ck) != 5 { // socket, port+proxy, env secret, no healthcheck, :latest; the finished job (_FILE secret) and db are clean
		t.Fatal(ck)
	}
	for _, f := range ck {
		if !reflect.DeepEqual(f.Containers, []string{"t"}) {
			t.Fatal(ck)
		}
	}
}

func TestRunbookAndMap(t *testing.T) {
	snap := Snapshot{
		"web": {Project: "app", ComposeDir: "/srv/app", Image: "nginx:1.27", State: "running", Restart: "always",
			Networks: []string{"proxy"}, Hosts: []string{"app.lan"}, Mounts: []string{"volume:data -> /data"}},
		"traefik": {Project: "proxy", Image: "traefik:3", State: "running", Restart: "always", Networks: []string{"proxy"}},
		"db":      {Project: "app", Image: "postgres:17", State: "exited", ExitCode: ptr(1), Restart: "always", Networks: []string{"proxy"}},
	}
	md := Runbook(snap, "", "box", "now", true, true)
	for _, want := range []string{"# Runbook: box", "**3 containers** in **2 projects**, 2 running.", "**Needs attention:** `db` (exited)",
		"```mermaid", "- **proxy** links 2 projects: app, proxy", "### app", "Reachable at: `app.lan`",
		"| `web` | `nginx:1.27` | running | - | volume:data -> /data |", "_No drift recorded yet._", "## Rebuild from scratch"} {
		if !strings.Contains(md, want) {
			t.Fatal(want, "\n", md)
		}
	}
	if strings.Contains(Runbook(snap, "", "box", "now", true, false), "## Rebuild from scratch") {
		t.Fatal("rebuild guide should be left out without a subscription")
	}
	for _, svg := range []string{MapSVG(snap, "a<b", false, false, ""), MapSVG(snap, "box", true, true, "x"), DemoMap(), DemoMapPhone(), FaviconSVG(), Lockup(30, "currentColor")} {
		if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
			t.Fatal(err, svg[:200])
		}
	}
	if s := MapSVG(snap, "a<b", false, false, ""); !strings.Contains(s, "a&lt;b") || !strings.Contains(s, `>1</tspan> shared networks`) {
		t.Fatal("map content")
	}
	if !strings.Contains(LiveDemo(), `class="live-map narrow"`) {
		t.Fatal("live demo")
	}
	for _, p := range demoPhone { // a typo would draw a project with no networks
		if _, ok := demo[p]; !ok {
			t.Fatal("demoPhone project not in demo:", p)
		}
	}
	safe := Safe(snap)
	b, _ := json.Marshal(safe)
	if strings.Contains(string(b), "nginx") || strings.Contains(string(b), "web") || len(Attention(safe)) != 1 {
		t.Fatal("public map leaks", string(b))
	}
}

// TestNormalize also guards the promise on the setup page: env values never leave the host, only names.
func TestNormalize(t *testing.T) {
	var in Inspect
	json.Unmarshal([]byte(`{"Name":"/web","Image":"sha256:0123456789abcdef","Config":{"Image":"nginx","Labels":{
	 "com.docker.compose.project":"app","traefik.http.routers.web.rule":"Host(`+"`a.lan`"+`) || Host(`+"`b.lan`"+`)"},"Env":["SECRET=x","PATH=/bin"]},
	 "State":{"Status":"running","ExitCode":0,"Health":{"Status":"healthy"}},"HostConfig":{"RestartPolicy":{"Name":"always"}},
	 "NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"8080"},{"HostIp":"::","HostPort":"8080"}]},"Networks":{"proxy":{},"db":{}}},
	 "Mounts":[{"Type":"volume","Name":"data","Destination":"/data"}]}`), &in)
	c := Normalize(in)
	b, _ := json.Marshal(c)
	want := `{"project":"app","compose_dir":"","image":"nginx","image_id":"0123456789ab","state":"running","exit_code":0,"health":"healthy",` +
		`"restart":"always","ports":["8080-\u003e80/tcp"],"networks":["db","proxy"],"mounts":["volume:data -\u003e /data"],"hosts":["a.lan","b.lan"],"env_keys":["PATH","SECRET"]}`
	if string(b) != want {
		t.Fatal(string(b))
	}
}

func TestRepoDigest(t *testing.T) {
	const d = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rds := []string{"mirror.lan/postgres@sha256:ffff", "postgres@" + d}
	for img, want := range map[string]string{
		"postgres:16": d, "docker.io/library/postgres:16": d, "postgres": d,
		"myapp:latest": "", // a local build or a retag: no digest to compare with the registry
	} {
		if got := RepoDigest(img, rds); got != want {
			t.Errorf("RepoDigest(%q) = %q, want %q", img, got, want)
		}
	}
}

func TestRecoveryPlan(t *testing.T) {
	d := "sha256:" + strings.Repeat("ab", 32)
	snap := Snapshot{
		"immich-server-1": {Project: "immich", ComposeDir: "/srv/immich", Image: "ghcr.io/immich-app/server:v1.2", RepoDigest: &d,
			State: "running", Restart: "unless-stopped", Ports: []string{"2283->2283/tcp", "53->53/udp"},
			Networks: []string{"immich_default", "proxy"}, Mounts: []string{"bind:/srv/photos -> /photos", "volume:immich_model -> /cache"},
			Hosts: []string{"photos.lan"}, EnvKeys: []string{"DB_PASSWORD"}},
		"traefik": {Project: "proxy", Image: "traefik:v3", State: "running", Restart: "always", Networks: []string{"proxy"},
			Mounts: []string{"bind:/var/run/docker.sock -> /var/run/docker.sock"}, Ports: []string{}, Hosts: []string{}, EnvKeys: []string{}},
	}
	r := RecoveryPlan(snap)
	if !slices.Equal(r.Data, []string{"/srv/photos", "immich_model (volume)"}) || !slices.Equal(r.Networks, []string{"proxy"}) {
		t.Fatalf("data %v networks %v", r.Data, r.Networks)
	}
	im := r.Projects[0].Compose
	for _, want := range []string{`"server":`, `image: "ghcr.io/immich-app/server:v1.2@` + d + `"`, `- "2283:2283"`, `- "53:53/udp"`,
		`- "/srv/photos:/photos"`, `"immich_model":`, `external: true`, "# environment had: DB_PASSWORD"} {
		if !strings.Contains(im, want) {
			t.Errorf("immich compose lacks %q:\n%s", want, im)
		}
	}
	if strings.Contains(im, "immich_default") {
		t.Error("compose's own default network must not be declared")
	}
	if md := RecoveryMD(r); !strings.Contains(md, "docker network create proxy") || !strings.Contains(md, "3. **Start each project.**") {
		t.Error(md)
	}
}
