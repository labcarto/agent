package lab

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
)

var esc = html.EscapeString

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func pos(x, y float64, has bool) string {
	if !has {
		return ""
	}
	return fmt.Sprintf(` x="%.0f" y="%.0f"`, x, y)
}

// Logo is the Labcarto mark: three rack units wired into one node (the map, in miniature).
func Logo(size int) string { return logo(size, 0, 0, false) }

func logo(size int, x, y float64, has bool) string {
	return fmt.Sprintf(`<svg class="logo"%s width="%d" height="%d" viewBox="0 0 32 32" aria-hidden="true">`, pos(x, y, has), size, size) +
		`<rect x=".5" y=".5" width="31" height="31" rx="7.5" fill="#0f172a" stroke="#334155"/>` +
		`<g fill="#e2e8f0"><rect x="5" y="7.5" width="12" height="4" rx="1.5"/><rect x="5" y="14" width="12" height="4" rx="1.5"/>` +
		`<rect x="5" y="20.5" width="12" height="4" rx="1.5"/></g>` +
		`<g fill="#22c55e"><circle cx="7.6" cy="9.5" r=".95"/><circle cx="7.6" cy="16" r=".95"/><circle cx="7.6" cy="22.5" r=".95"/></g>` +
		`<path d="M17 9.5C21.5 9.5 21 16 25 16M17 16H25M17 22.5C21.5 22.5 21 16 25 16" stroke="#22c55e" stroke-width="1.6" fill="none"/>` +
		`<circle cx="25" cy="16" r="3" fill="#22c55e"/></svg>`
}

// LockupW: the lockup viewBox is 172x40 (52 + wordmark advance 4144 * 0.028909).
const LockupW = 172

// Lockup is the full logo: mark + "Labcarto" wordmark, one SVG. lab colours the "Lab" half (theme-dependent).
func Lockup(height float64, lab string) string { return lockup(height, 0, 0, false, lab) }

func lockup(height, x, y float64, has bool, lab string) string {
	return fmt.Sprintf(`<svg class="lockup"%s width="%.1f" height="%s" viewBox="0 0 %d 40" aria-hidden="true">`, pos(x, y, has), height*LockupW/40, num(height), LockupW) +
		strings.Replace(logo(40, 0, 0, true), `class="logo" `, "", 1) +
		`<g transform="translate(52 10.47) scale(0.028909)"><path fill="` + lab + `" d="` + wmLab + `"/>` +
		`<path fill="#22c55e" style="fill:var(--wm-accent,#22c55e)" d="` + wmCarto + `"/></g></svg>`
}

// FaviconSVG is the mark as a standalone SVG document.
func FaviconSVG() string {
	return strings.Replace(Logo(32), `class="logo" `, `xmlns="http://www.w3.org/2000/svg" `, 1)
}

var badgeW = math.Round(36*LockupW/40.0) + 24 // lockup at 36px plus 12px padding each side

var hubColors = []string{"#22c55e", "#60a5fa", "#f59e0b", "#f472b6", "#a78bfa", "#2dd4bf"}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// MapSVG draws project cards wired to shared-network hubs. Dark, for sharing.
// Wide (1200px): two card columns, hubs in the middle. Narrow (480px, phones): one card column, hubs on the right.
// live adds classes for the animation and data-p/data-n hooks for map.js; the static output is unchanged.
// uid keeps the <defs> ids of several maps on one page apart.
func MapSVG(snap Snapshot, host string, live, narrow bool, uid string) string {
	netsOf, users, hubs := Topology(snap)
	color := map[string]string{}
	for i, n := range hubs {
		color[n] = hubColors[i%len(hubColors)]
	}
	count, bad := map[string]int{}, map[string]int{}
	for _, c := range snap {
		count[c.Project]++
		if Trouble(c) {
			bad[c.Project]++
		}
	}
	// cluster projects by their first hub so the wires stay short
	first := func(p string) int {
		for i, n := range hubs {
			if netsOf[p][n] {
				return i
			}
		}
		return len(hubs)
	}
	projects := Sorted(netsOf)
	sort.SliceStable(projects, func(i, j int) bool { return first(projects[i]) < first(projects[j]) })

	row, ch := 54.0, 40.0
	W, m, top, cw, hw, hcx, maxname, maxhub := 1200.0, 48.0, 196.0, 300.0, 208.0, 600.0, 24, 20
	perCol := (len(projects) + 1) / 2
	if narrow {
		row, ch = 46, 36
		W, m, top, cw, hw, hcx, maxname, maxhub = 480, 24, 236, 230, 150, 381, 18, 16
		perCol = len(projects)
	}
	midH := float64(perCol)*row - (row - ch)
	gap := math.Max(64, math.Min(120, midH/math.Max(float64(len(hubs)), 1))) // 52px pills + 12px air: never overlap
	span := float64(len(hubs)-1)*gap + 52
	hubMid := top + span/2 // more hubs than the cards are tall: start at the top
	if span <= midH {
		hubMid = top + midH/2 + ch/2
	}
	hubY := map[string]float64{}
	for i, n := range hubs {
		hubY[n] = hubMid + (float64(i)-float64(len(hubs)-1)/2)*gap
	}
	minH, pad := 720.0, 96.0
	if narrow {
		minH, pad = 560, 64
		uid = "n" + uid
	}
	H := math.Max(minH, top+math.Max(float64(perCol)*row, span)+pad)

	var wires, cards, hubsSVG []string
	for i, p := range projects {
		left := i < perCol
		x, y := W-m-cw, top+float64(i-perCol)*row
		px := x
		if left {
			x, y = m, top+float64(i)*row
			px = x + cw
		}
		py := y + ch/2
		var linked []string
		for _, n := range hubs {
			if netsOf[p][n] {
				linked = append(linked, n)
			}
		}
		for _, n := range linked {
			hx, hy := hcx+hw/2, hubY[n]
			if left {
				hx = hcx - hw/2
			}
			mx := (px + hx) / 2
			d := fmt.Sprintf("M%.0f %.0f C%.0f %.0f %.0f %.0f %.0f %.0f", px, py, mx, py, mx, hy, hx, hy)
			k := float64(len(wires))
			w := ""
			if live {
				w = fmt.Sprintf(`<g class="lk" data-p="%s" data-n="%s">`, esc(p), esc(n))
			}
			anim := ""
			if live {
				anim = fmt.Sprintf(` class="w" pathLength="1" style="animation-delay:%.2fs"`, .4+.04*k)
			}
			w += fmt.Sprintf(`<path d="%s"%s stroke="%s" stroke-opacity=".5" stroke-width="1.75" fill="none"/>`, d, anim, color[n]) +
				fmt.Sprintf(`<circle cx="%.0f" cy="%.0f" r="3.5" fill="%s"/>`, px, py, color[n])
			if live {
				ki := int(k)
				w += fmt.Sprintf(`<path d="%s" class="f" pathLength="1" stroke="%s" stroke-width="3" stroke-linecap="round" fill="none" `+
					`style="animation-duration:%.1fs;animation-delay:%.2fs"/></g>`, d, color[n], 2.4+float64(ki*7%11)/5, 1.6+float64(ki*5%13)/4)
			}
			wires = append(wires, w)
		}
		accent := "#3a4558"
		if len(linked) > 0 {
			accent = color[linked[0]]
		}
		state, blink := "#22c55e", ""
		if bad[p] > 0 {
			state = "#f59e0b"
			if live {
				blink = ` class="blink"`
			}
		}
		g := "<g>"
		if live {
			g = fmt.Sprintf(`<g class="c" data-p="%s" style="animation-delay:%.2fs">`, esc(p), .03*float64(i))
		}
		cards = append(cards, g+fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="%s" rx="10" fill="#111827" stroke="#1f2937"/>`, num(x), num(y), num(cw), num(ch))+
			fmt.Sprintf(`<rect x="%s" y="%s" width="3" height="%s" rx="1.5" fill="%s"/>`, num(x), num(y+10), num(ch-20), accent)+
			fmt.Sprintf(`<circle cx="%s" cy="%s" r="4" fill="%s"%s/>`, num(x+22), num(y+ch/2), state, blink)+
			fmt.Sprintf(`<text x="%s" y="%s" dominant-baseline="central" font-size="15" font-weight="500" fill="#e5e7eb">%s</text>`, num(x+36), num(y+ch/2), esc(truncate(p, maxname)))+
			fmt.Sprintf(`<text x="%s" y="%s" dominant-baseline="central" text-anchor="end" font-size="12.5" `, num(x+cw-14), num(y+ch/2))+
			fmt.Sprintf(`font-family="JetBrains Mono, ui-monospace, monospace" fill="#9ca3af">%d</text></g>`, count[p]))
	}
	for _, n := range hubs {
		hx, hy, r := hcx-hw/2, hubY[n], 26.0
		g, glow := "<g>", ""
		if live {
			g, glow = fmt.Sprintf(`<g class="h" data-n="%s">`, esc(n)), ` class="glow"`
		}
		hubsSVG = append(hubsSVG, g+fmt.Sprintf(`<rect x="%.0f" y="%.0f" width="%s" height="52" rx="26" fill="%s" opacity=".18" filter="url(#glow%s)"%s/>`, hx, hy-r, num(hw), color[n], uid, glow)+
			fmt.Sprintf(`<rect x="%.0f" y="%.0f" width="%s" height="52" rx="26" fill="#0b1220" stroke="%s" stroke-width="1.5"/>`, hx, hy-r, num(hw), color[n])+
			fmt.Sprintf(`<text x="%s" y="%.0f" text-anchor="middle" font-size="15" font-weight="600" fill="%s">%s</text>`, num(hcx), hy-5, color[n], esc(cut(n, maxhub)))+
			fmt.Sprintf(`<text x="%s" y="%.0f" text-anchor="middle" font-size="11.5" fill="#9ca3af">%d projects</text></g>`, num(hcx), hy+13, len(users[n])))
	}

	stats := []struct {
		v int
		k string
	}{{len(snap), "containers"}, {len(projects), "projects"}, {Running(snap), "running"}, {len(hubs), "shared networks"}}
	var chips []string
	sx, cy := m, 120.0
	if narrow {
		cy = 146
	}
	for j, st := range stats {
		if narrow && j == 2 {
			sx, cy = m, cy+38
		}
		label := fmt.Sprintf("%d %s", st.v, st.k)
		w := 22 + 8.2*float64(len([]rune(label)))
		chips = append(chips, fmt.Sprintf(`<rect x="%s" y="%s" width="%.0f" height="30" rx="15" fill="#111827" stroke="#1f2937"/>`, num(sx), num(cy), w)+
			fmt.Sprintf(`<text x="%s" y="%s" font-size="13.5" fill="#9ca3af"><tspan fill="#e5e7eb" font-weight="600">%d</tspan> %s</text>`, num(sx+14), num(cy+20), st.v, st.k))
		sx += w + 8
	}
	var head string
	if narrow {
		head = lockup(28, m, 26, true, "#f3f4f6") +
			fmt.Sprintf(`<text x="%s" y="94" font-size="12" font-weight="600" letter-spacing="2" fill="#22c55e">LAB MAP</text>`, num(m)) +
			fmt.Sprintf(`<text x="%s" y="128" font-size="28" font-weight="700" letter-spacing="-.8" fill="#f3f4f6">%s</text>`, num(m), esc(host))
	} else {
		head = fmt.Sprintf(`<text x="%s" y="66" font-size="13" font-weight="600" letter-spacing="2" fill="#22c55e">LAB MAP</text>`, num(m)) +
			fmt.Sprintf(`<text x="%s" y="104" font-size="34" font-weight="700" letter-spacing="-1" fill="#f3f4f6">%s</text>`, num(m), esc(host)) +
			fmt.Sprintf(`<rect x="%s" y="52" width="%s" height="56" rx="14" fill="#0b1220" stroke="#1f2937"/>`, num(W-m-badgeW), num(badgeW)) +
			lockup(36, W-m-badgeW+12, 62, true, "#f3f4f6")
	}
	cls := ""
	if live {
		kind := " wide"
		if narrow {
			kind = " narrow"
		}
		cls = fmt.Sprintf(` class="live-map%s" role="img" aria-label="Lab map of %s"`, kind, esc(host))
	}
	foot := 36.0
	if narrow {
		foot = 28
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg"%s width="%s" height="%.0f" viewBox="0 0 %s %.0f" font-family="IBM Plex Sans, Inter, system-ui, -apple-system, Segoe UI, sans-serif">
<defs><radialGradient id="bg%[6]s" cx="50%%" cy="0%%" r="90%%"><stop offset="0" stop-color="#132036"/><stop offset="1" stop-color="#070b12"/></radialGradient>
<pattern id="dots%[6]s" width="24" height="24" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r="1" fill="#ffffff" opacity=".045"/></pattern>
<filter id="glow%[6]s" x="-50%%" y="-100%%" width="200%%" height="300%%"><feGaussianBlur stdDeviation="10"/></filter></defs>
<rect width="%[2]s" height="%.0[3]f" fill="url(#bg%[6]s)"/><rect width="%[2]s" height="%.0[3]f" fill="url(#dots%[6]s)"/>
%[7]s
%[8]s
%[9]s%[10]s%[11]s
<text x="%[12]s" y="%.0[13]f" font-size="13" fill="#9ca3af"><tspan fill="#22c55e">●</tspan> healthy<tspan dx="20" fill="#f59e0b">●</tspan> needs attention<tspan dx="20">#</tspan> containers</text>
</svg>`, cls, num(W), H, num(W), H, uid, head, strings.Join(chips, ""), strings.Join(wires, ""), strings.Join(cards, ""), strings.Join(hubsSVG, ""), num(m), H-foot)
}

// MapPair is the wide map plus the phone layout; the .duo wrapper's CSS shows the one that fits the screen.
func MapPair(snap Snapshot, host, uid string) string {
	return MapSVG(snap, host, true, false, uid) + MapSVG(snap, host, true, true, uid)
}

// MapData is the click-for-details payload of the overview map (never on the public map):
// per project [name, image, status, health, ports], and the projects of each hub.
func MapData(snap Snapshot) map[string]any {
	_, users, hubs := Topology(snap)
	p := map[string][]any{}
	for _, k := range Sorted(snap) {
		c := snap[k]
		ports := c.Ports
		if ports == nil {
			ports = []string{}
		}
		p[c.Project] = append(p[c.Project], []any{k, c.Image, Status(c), c.Health, ports})
	}
	n := map[string][]string{}
	for _, h := range hubs {
		n[h] = Sorted(users[h])
	}
	return map[string]any{"p": p, "n": n}
}

// Safe keeps only what the public map shows: project, networks and health, under anonymous keys.
// Container names, images, ports, paths and env keys never reach that page.
func Safe(snap Snapshot) Snapshot {
	out := Snapshot{}
	for i, k := range Sorted(snap) {
		c := snap[k]
		out[strconv.Itoa(i)] = Container{Project: c.Project, Networks: c.Networks, State: Status(c), Health: c.Health}
	}
	return out
}

var demo = map[string][]string{"traefik": {"proxy"}, "authentik": {"proxy", "db"}, "immich": {"proxy", "db"}, "nextcloud": {"proxy", "db"},
	"paperless": {"proxy", "db"}, "gitea": {"proxy", "db"}, "vaultwarden": {"proxy"}, "jellyfin": {"proxy"},
	"home-assistant": {"proxy"}, "mealie": {"proxy", "db"}, "postgres": {"db"}, "grafana": {"proxy", "monitoring", "db"},
	"prometheus": {"monitoring"}, "node-exporter": {"monitoring"}, "uptime-kuma": {"proxy", "monitoring"},
	"adguard": {}, "frigate": {}, "syncthing": {}}

var liveDemo = map[string][]string{"traefik": {"proxy"}, "authentik": {"proxy", "db"}, "immich": {"proxy", "db", "ml"}, "nextcloud": {"proxy", "db"},
	"paperless-ngx": {"proxy", "db"}, "gitea": {"proxy", "db"}, "vaultwarden": {"proxy"}, "jellyfin": {"proxy", "media"},
	"sonarr": {"media"}, "radarr": {"media"}, "prowlarr": {"media"}, "qbittorrent": {"media"},
	"home-assistant": {"proxy", "iot"}, "mosquitto": {"iot"}, "zigbee2mqtt": {"iot"}, "frigate": {"iot", "ml"},
	"postgres": {"db"}, "grafana": {"proxy", "monitoring", "db"}, "prometheus": {"monitoring"},
	"node-exporter": {"monitoring"}, "uptime-kuma": {"proxy", "monitoring"}, "ollama": {"ml"},
	"adguard-home": {}, "syncthing": {}}

// phones get a 12-project subset so the single-column map stays about one screen tall
var phone = map[string]bool{"traefik": true, "authentik": true, "immich": true, "paperless-ngx": true, "jellyfin": true, "sonarr": true,
	"qbittorrent": true, "home-assistant": true, "frigate": true, "postgres": true, "grafana": true, "ollama": true}

// DemoMap is the landing page's static hero map; LiveDemo its animated pair.
func DemoMap() string {
	s := Snapshot{}
	for p, n := range demo {
		s[p] = Container{Project: p, Networks: n, State: "running"}
	}
	return MapSVG(s, "homelab", false, false, "")
}

// demoPhone: the hero map's phone layout keeps 11 projects and all three networks, so it stays about one screen tall
var demoPhone = []string{"traefik", "authentik", "immich", "nextcloud", "vaultwarden", "jellyfin", "postgres", "grafana", "prometheus", "uptime-kuma", "adguard"}

// DemoMapPhone is DemoMap's single-column layout for phones (the landing <picture> swaps it in at 480px and below:
// wider, the 480px-wide map would scale up past a screen's height).
func DemoMapPhone() string {
	s := Snapshot{}
	for _, p := range demoPhone {
		s[p] = Container{Project: p, Networks: demo[p], State: "running"}
	}
	return MapSVG(s, "homelab", false, true, "")
}

func LiveDemo() string {
	all, small := liveDemoSnaps()
	return MapSVG(all, "homelab", true, false, "") + MapSVG(small, "homelab", true, true, "")
}

// LiveDemoData is the landing map's hover-for-details payload (same shape as MapData).
func LiveDemoData() map[string]any {
	all, _ := liveDemoSnaps()
	return MapData(all)
}

func liveDemoSnaps() (all, small Snapshot) {
	all, small = Snapshot{}, Snapshot{}
	for p, n := range liveDemo {
		for k := 0; k < 1+len(p)%3; k++ {
			c := Container{Project: p, Networks: n, State: "running", Image: p + ":latest"}
			if p == "qbittorrent" && k > 0 {
				c.State = "exited"
			}
			if p == "frigate" && k > 0 {
				c.Health = "unhealthy"
			}
			name := fmt.Sprintf("%s-%d", p, k)
			all[name] = c
			if phone[p] {
				small[name] = c
			}
		}
	}
	return all, small
}
