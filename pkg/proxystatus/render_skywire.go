// Package proxystatus pkg/proxystatus/render_skywire.go
package proxystatus

import (
	"fmt"
	"html"
	"strings"

	"github.com/0magnet/bitree"
)

// writeRouteTree writes the bilateral route tree for snap's tunnels: the
// tunnel census, the layer legend when there is more than one tunnel, and the
// tree itself. Shared by a proxy surface's mux section and each consumer on
// the skywire surface.
func writeRouteTree(b *strings.Builder, snap Snapshot) {
	hLeft, hLabel, hCols := TreeHeader()
	// Per-hop color classes, keyed by PK: the exit red, each intermediate hop
	// LEVEL its own hue (see hopClassMap). Captured in the StyleCell closure so the
	// PK label cells can be wrapped without disturbing bitree's plain-text layout.
	hopClasses := hopClassMap(snap)
	// The tunnel census, above the tree: how many route groups this proxy holds
	// and how they are SPLIT by role. Inside the live region, so each ~1s push
	// re-renders it from the fresh snapshot.
	writeTunnelCount(b, snap)
	// When more than one stream is present, name the two multiplexing LAYERS above
	// the tree so the stream-boundary nodes and per-stream leg bands below are
	// self-explanatory.
	if len(snap.Tunnels) > 1 {
		writeLayerLegend(b)
	}
	b.WriteString(`<div class="tree">`)
	b.WriteString(`<pre class="bitree">`)
	b.WriteString(bitree.Render(RouteTree(snap), bitree.Options{
		StyleCell: func(text string, kind bitree.CellKind) string {
			return htmlStyleCell(text, kind, hopClasses)
		},
		HeaderLeft:  hLeft,
		HeaderLabel: hLabel,
		HeaderCols:  hCols,
	}))
	b.WriteString(`</pre>`)
	b.WriteString(`</div>`)
}

// consumerSnapshot is the view of one consumer that the route tree renders.
func consumerSnapshot(snap Snapshot, c Consumer) Snapshot {
	sub := Snapshot{Surface: snap.Surface, SelfPK: snap.SelfPK, Tunnels: c.Tunnels}
	if len(c.Tunnels) > 0 {
		sub.MuxEnabled = c.Tunnels[0].MuxEnabled
		sub.Legs = c.Tunnels[0].Legs
	}
	return sub
}

// writeConsumersSection is the skywire surface's content: a table of every
// app and subsystem holding a route or a direct stream, then each one's route
// tree.
func writeConsumersSection(b *strings.Builder, snap Snapshot) {
	if snap.Surface != SurfaceSkywire {
		return
	}
	fmt.Fprintf(b, `<section><h2>route users <small>%d</small></h2>`, len(snap.Consumers))
	if len(snap.Consumers) == 0 {
		b.WriteString(`<p class="empty">Nothing on this visor holds a route or a direct stream right now.</p></section>`)
		return
	}
	b.WriteString(`<table class="strm"><thead><tr><th>user</th><th>state</th><th class="num">tunnels</th>` +
		`<th class="num">live legs</th><th class="num">↑ sent</th><th class="num">↓ recv</th><th>far end</th></tr></thead><tbody>`)
	for _, c := range snap.Consumers {
		var legs int
		var sent, recv uint64
		ends := map[string]struct{}{}
		var endList []string
		for _, t := range c.Tunnels {
			if _, ok := ends[t.ExitPK]; !ok {
				ends[t.ExitPK] = struct{}{}
				endList = append(endList, t.ExitPK)
			}
			for _, l := range t.Legs {
				if l.Alive {
					legs++
				}
				sent += l.SentBytes
				recv += l.RecvBytes
			}
		}
		state := "stopped"
		switch {
		case c.Inbound:
			state = "accepted"
		case c.Running:
			state = "running"
		case c.Name == "visor":
			state = "running"
		}
		fmt.Fprintf(b, `<tr><td>%s</td><td>%s</td><td class="num">%d</td><td class="num">%d</td>`+
			`<td class="num">%s</td><td class="num">%s</td><td>`,
			html.EscapeString(c.Name), state, len(c.Tunnels), legs, compactBytes(sent), compactBytes(recv))
		for i, pk := range endList {
			if i > 0 {
				b.WriteString(`<br>`)
			}
			fmt.Fprintf(b, `<code data-copy="%s">%s</code>`, html.EscapeString(pk), html.EscapeString(pk))
		}
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table></section>`)

	for _, c := range snap.Consumers {
		fmt.Fprintf(b, `<section><h2>%s</h2>`, html.EscapeString(c.Name))
		writeRouteTree(b, consumerSnapshot(snap, c))
		b.WriteString(`</section>`)
	}
	writeTreeLegend(b)
}
