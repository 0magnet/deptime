package deptime

import (
	"fmt"
	"html"
	"strings"
)

// RenderOptions is how the animation is drawn.
type RenderOptions struct {
	FPS     int     // frames a second
	Width   int     // 0 = the layout's natural size
	Stroke  string  // edge color
	NodeFil string  // node fill
	NodeStr string  // node outline
	Text    string  // label color
	BG      string  // background
	NewFill string  // a node's fill on the frame it first appears
	Caption bool    // draw the date and commit under the graph
	Pad     float64 // border around the layout, in points
}

// DefaultRender is a dark sheet that reads at a glance.
func DefaultRender() RenderOptions {
	return RenderOptions{
		FPS: 4, Stroke: "#27405a", NodeFil: "#0f1722", NodeStr: "#2f4964",
		Text: "#9fb4c7", BG: "#0a0d14", NewFill: "#1d4e63", Caption: true, Pad: 24,
	}
}

// Render writes the animated SVG.
//
// Every node and edge is emitted ONCE, at the position the union layout gave
// it, and carries its own visibility animation. Nothing moves; things appear
// and disappear. See layout.go for why that is the only way this is legible.
func Render(frames []Graph, u *Union, p *Placed, o RenderOptions) string {
	if o.FPS <= 0 {
		o.FPS = 4
	}
	n := len(frames)
	dur := float64(n) / float64(o.FPS)
	w, h := p.W+2*o.Pad, p.H+2*o.Pad
	capH := 0.0
	if o.Caption {
		capH = 34
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="system-ui,sans-serif">`,
		w, h+capH, w, h+capH)
	fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" fill="%s"/>`, w, h+capH, o.BG)
	fmt.Fprintf(&b, `<g transform="translate(%.1f,%.1f)">`, o.Pad, o.Pad)

	// Edges first so nodes sit on top of them.
	fmt.Fprintf(&b, `<g fill="none" stroke="%s" stroke-width="0.7" stroke-opacity="0.55">`, o.Stroke)
	for _, e := range u.Edges {
		pts := p.Edge[e]
		if len(pts) < 2 {
			continue
		}
		b.WriteString(`<path d="` + bezier(pts) + `"`)
		writeVisibility(&b, u.EdgeIn[e], dur)
		b.WriteString(`</path>`)
	}
	b.WriteString(`</g>`)

	// Nodes.
	for _, name := range u.Nodes {
		box, ok := p.Node[name]
		if !ok {
			continue
		}
		b.WriteString(`<g`)
		writeVisibility(&b, u.NodeIn[name], dur)
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s" stroke="%s" stroke-width="0.8"/>`,
			box.X-box.W/2, box.Y-box.H/2, box.W, box.H, o.NodeFil, o.NodeStr)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="%.1f" fill="%s" text-anchor="middle" dominant-baseline="central">%s</text>`,
			box.X, box.Y, labelSize(box), o.Text, html.EscapeString(shortName(name)))
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g>`)

	if o.Caption {
		writeCaption(&b, frames, u, w, h, capH, dur, o)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// writeVisibility closes the opening tag with an opacity animation, emitting
// only the moments the element CHANGES.
//
// The obvious encoding is one value per frame — "0;0;1;1;1;…" — and it is what
// the first version did. It costs every element the frame count in characters,
// which on a graph of two thousand elements over two hundred frames is most of
// a megabyte of semicolons. A package almost always appears once and stays, so
// what is actually being said is "invisible, then visible from here": two
// stops and two key times, whatever the frame count. Elements that come and go
// pay a stop each way.
func writeVisibility(b *strings.Builder, in []bool, dur float64) {
	n := len(in)
	if n == 0 {
		b.WriteString(` opacity="0">`)
		return
	}
	// Always on: no animation at all.
	always := true
	for _, v := range in {
		if !v {
			always = false
			break
		}
	}
	if always {
		b.WriteString(`>`)
		return
	}
	var vals, times []string
	prev := false
	for i, v := range in {
		if i == 0 || v != prev {
			vals = append(vals, boolStr(v))
			times = append(times, fmt.Sprintf("%.5g", float64(i)/float64(n)))
			prev = v
		}
	}
	// keyTimes must start at 0 and end at 1.
	times[0] = "0"
	vals = append(vals, vals[len(vals)-1])
	times = append(times, "1")
	fmt.Fprintf(b, ` opacity="%s"><animate attributeName="opacity" calcMode="discrete" dur="%gs" repeatCount="indefinite" values="%s" keyTimes="%s"/>`,
		boolStr(in[0]), dur, strings.Join(vals, ";"), strings.Join(times, ";"))
}

func boolStr(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// writeCaption draws the date, the commit and the running counts, each as its
// own text element shown on its own frame. It is the only part that is one
// element per frame, and it has to be: the text is different every time.
func writeCaption(b *strings.Builder, frames []Graph, u *Union, w, h, capH, dur float64, o RenderOptions) {
	n := len(frames)
	fmt.Fprintf(b, `<g font-size="13" fill="%s">`, o.Text)
	for i, g := range frames {
		vis := make([]bool, n)
		vis[i] = true
		edges := 0
		for _, to := range g.Edges {
			edges += len(to)
		}
		b.WriteString(`<g`)
		writeVisibility(b, vis, dur)
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f">%s · %s</text>`,
			o.Pad, h+capH/2, g.When, html.EscapeString(g.Commit))
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" text-anchor="end">%d packages · %d edges</text>`,
			w-o.Pad, h+capH/2, len(g.Nodes), edges)
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g>`)
	_ = u
}

// bezier turns graphviz's spline control points into a path. The plain format
// gives the points of a cubic B-spline: the first, then groups of three.
func bezier(pts [][2]float64) string {
	var d strings.Builder
	fmt.Fprintf(&d, "M%.1f,%.1f", pts[0][0], pts[0][1])
	i := 1
	for ; i+2 < len(pts); i += 3 {
		fmt.Fprintf(&d, "C%.1f,%.1f %.1f,%.1f %.1f,%.1f",
			pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1], pts[i+2][0], pts[i+2][1])
	}
	for ; i < len(pts); i++ {
		fmt.Fprintf(&d, "L%.1f,%.1f", pts[i][0], pts[i][1])
	}
	return d.String()
}

func labelSize(b Box) float64 {
	s := b.H * 0.42
	if s > 9 {
		s = 9
	}
	if s < 4 {
		s = 4
	}
	return s
}
