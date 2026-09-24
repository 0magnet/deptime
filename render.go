package deptime

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
)

// RenderOptions is how the animation is drawn.
type RenderOptions struct {
	FPS      int     // frames a second
	Hold     float64 // seconds the final frame stays up before the loop
	Stroke   string  // edge color
	NodeFil  string  // node fill
	NodeStr  string  // node outline
	Text     string  // label color
	BG       string  // background
	NewFill  string  // a node's fill on the frame it first appears
	NewStr   string  // and its outline
	Timeline bool    // draw the timeline and caption above the graph
	Band     string  // timeline background
	Area     string  // package count, ahead of the cursor
	Past     string  // package count, behind it
	Cursor   string  // the cursor and the rug of frame ticks under it
	Pad      float64 // border around the layout, in points
}

// DefaultRender is a dark sheet that reads at a glance.
func DefaultRender() RenderOptions {
	return RenderOptions{
		FPS: 6, Hold: 3,
		Stroke: "#27405a", NodeFil: "#0f1722", NodeStr: "#2f4964",
		Text: "#9fb4c7", BG: "#0a0d14", NewFill: "#1d5a73", NewStr: "#f2b84b",
		Timeline: true, Band: "#0e1520", Area: "#16283a", Past: "#2f6f8f", Cursor: "#f2b84b",
		Pad: 24,
	}
}

// clock is when each frame starts, as a fraction of the loop.
//
// Every frame gets the same time on screen — a frame is a change to the graph,
// and a change in a busy week is worth as much as one after a quiet year —
// except the last, which holds, so the loop does not snap back to the start
// the instant it arrives at the present.
type clock struct {
	start []float64 // fraction of dur at which frame i appears
	dur   float64   // seconds
}

func newClock(n, fps int, hold float64) clock {
	if fps <= 0 {
		fps = 6
	}
	if hold < 0 {
		hold = 0
	}
	step := 1 / float64(fps)
	c := clock{dur: float64(n)*step + hold, start: make([]float64, n)}
	for i := range c.start {
		c.start[i] = float64(i) * step / c.dur
	}
	return c
}

// Render writes the animated SVG.
//
// Every node and edge is emitted ONCE, at the position the union layout gave
// it, and carries its own visibility animation. Nothing moves; things appear
// and disappear. See layout.go for why that is the only way this is legible.
func Render(frames []Graph, u *Union, p *Placed, o RenderOptions) string {
	n := len(frames)
	c := newClock(n, o.FPS, o.Hold)
	w, h := p.W+2*o.Pad, p.H+2*o.Pad
	// Text and the timeline scale with the drawing, or a wide graph shrinks
	// them to nothing once the browser fits it to a window.
	s := math.Max(1, math.Min(4, w/900))
	top := 0.0
	if o.Timeline {
		top = 96 * s
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="system-ui,sans-serif">`,
		w, h+top, w, h+top)
	fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" fill="%s"/>`, w, h+top, o.BG)
	if o.Timeline {
		writeTimeline(&b, frames, w, s, c, o)
	}
	fmt.Fprintf(&b, `<g transform="translate(%.1f,%.1f)">`, o.Pad, o.Pad+top)

	// Edges first so nodes sit on top of them.
	fmt.Fprintf(&b, `<g fill="none" stroke="%s" stroke-width="0.7" stroke-opacity="0.55">`, o.Stroke)
	for _, e := range u.Edges {
		pts := p.Edge[e]
		if len(pts) < 2 {
			continue
		}
		b.WriteString(`<path d="` + bezier(pts) + `"`)
		writeVisibility(&b, u.EdgeIn[e], c)
		b.WriteString(`</path>`)
	}
	b.WriteString(`</g>`)

	// Nodes.
	for _, name := range u.Nodes {
		box, ok := p.Node[name]
		if !ok {
			continue
		}
		in := u.NodeIn[name]
		b.WriteString(`<g`)
		writeVisibility(&b, in, c)
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s" stroke="%s" stroke-width="0.8"/>`,
			box.X-box.W/2, box.Y-box.H/2, box.W, box.H, o.NodeFil, o.NodeStr)
		// Lit on the frame it arrives. With hundreds of frames a package
		// appearing is otherwise one small box among hundreds, easily missed.
		if fresh := arrivals(in); fresh != nil {
			b.WriteString(`<rect`)
			fmt.Fprintf(&b, ` x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s" stroke="%s" stroke-width="1.4"`,
				box.X-box.W/2, box.Y-box.H/2, box.W, box.H, o.NewFill, o.NewStr)
			writeVisibility(&b, fresh, c)
			b.WriteString(`</rect>`)
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="%.1f" fill="%s" text-anchor="middle" dominant-baseline="central">%s</text>`,
			box.X, box.Y, labelSize(box), o.Text, html.EscapeString(shortName(name)))
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

// arrivals is true on the frames where a node appears having been absent the
// frame before. The first frame is not an arrival: everything is new there,
// and lighting the whole graph says nothing.
func arrivals(in []bool) []bool {
	var out []bool
	for i := 1; i < len(in); i++ {
		if in[i] && !in[i-1] {
			if out == nil {
				out = make([]bool, len(in))
			}
			out[i] = true
		}
	}
	return out
}

// writeTimeline draws the band across the top: package count over real time,
// a tick for every frame, year lines, and a cursor that jumps to the frame on
// screen, with the frame's date, commit and subject written above it.
//
// The x axis is the calendar, not the frame number. Frames are spaced evenly
// in TIME ON SCREEN, so the cursor races across a quiet year and crawls
// through a busy month — which is the information: where the rug is dense,
// the structure was being worked on.
func writeTimeline(b *strings.Builder, frames []Graph, w, s float64, c clock, o RenderOptions) {
	n := len(frames)
	left, right := 16*s, w-16*s
	capY := 22 * s
	bandY, bandH := 36*s, 44*s
	t := make([]float64, n)
	for i, g := range frames {
		if d, err := time.Parse("2006-01-02", g.When); err == nil {
			t[i] = float64(d.Unix())
		}
	}
	t0, t1 := t[0], t[n-1]
	x := func(i int) float64 {
		if t1 <= t0 {
			if n == 1 {
				return right
			}
			return left + (right-left)*float64(i)/float64(n-1)
		}
		return left + (right-left)*(t[i]-t0)/(t1-t0)
	}
	maxN := 1
	for _, g := range frames {
		maxN = max(maxN, len(g.Nodes))
	}
	y := func(k int) float64 { return bandY + bandH - bandH*0.9*float64(k)/float64(maxN) }

	fmt.Fprintf(b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, left, bandY, right-left, bandH, o.Band)

	// Package count as a step area: the count holds until the next change.
	var area strings.Builder
	fmt.Fprintf(&area, "M%.1f,%.1f", x(0), bandY+bandH)
	for i, g := range frames {
		fmt.Fprintf(&area, "L%.1f,%.1f", x(i), y(len(g.Nodes)))
		if i+1 < n {
			fmt.Fprintf(&area, "L%.1f,%.1f", x(i+1), y(len(g.Nodes)))
		}
	}
	fmt.Fprintf(&area, "L%.1f,%.1fZ", x(n-1), bandY+bandH)
	fmt.Fprintf(b, `<path d="%s" fill="%s"/>`, area.String(), o.Area)

	// The same area again, clipped to the past: a rectangle whose width
	// follows the cursor. One animated element, not one per frame.
	xs := make([]string, n)
	ws := make([]string, n)
	for i := range frames {
		xs[i] = fmt.Sprintf("%.1f,0", x(i))
		ws[i] = fmt.Sprintf("%.1f", x(i)-left)
	}
	keys := keyTimes(c)
	fmt.Fprintf(b, `<clipPath id="past"><rect x="%.1f" y="%.1f" height="%.1f" width="%s">`, left, bandY, bandH, ws[0])
	fmt.Fprintf(b, `<animate attributeName="width" calcMode="discrete" dur="%gs" repeatCount="indefinite" values="%s" keyTimes="%s"/></rect></clipPath>`,
		c.dur, strings.Join(ws, ";"), keys)
	fmt.Fprintf(b, `<path d="%s" fill="%s" clip-path="url(#past)"/>`, area.String(), o.Past)

	// Year lines over the area, labeled at the foot of the band.
	if t1 > t0 {
		y0, y1 := time.Unix(int64(t0), 0).UTC().Year(), time.Unix(int64(t1), 0).UTC().Year()
		fmt.Fprintf(b, `<g font-size="%.1f" fill="%s" fill-opacity="0.7">`, 9*s, o.Text)
		for yr := y0 + 1; yr <= y1; yr++ {
			ty := float64(time.Date(yr, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
			xx := left + (right-left)*(ty-t0)/(t1-t0)
			fmt.Fprintf(b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="%s" stroke-opacity="0.3" stroke-width="%.1f"/>`,
				xx, xx, bandY, bandY+bandH, o.Text, 0.6*s)
			fmt.Fprintf(b, `<text x="%.1f" y="%.1f">%d</text>`, xx+3*s, bandY+10*s, yr)
		}
		b.WriteString(`</g>`)
	}

	// The rug: where the graph changed.
	var rug strings.Builder
	for i := range frames {
		fmt.Fprintf(&rug, "M%.1f,%.1fv%.1f", x(i), bandY+bandH, 5*s)
	}
	fmt.Fprintf(b, `<path d="%s" stroke="%s" stroke-opacity="0.45" stroke-width="%.1f"/>`, rug.String(), o.Cursor, 0.6*s)

	// The cursor.
	fmt.Fprintf(b, `<g transform="translate(%s)">`, xs[0])
	fmt.Fprintf(b, `<animateTransform attributeName="transform" type="translate" calcMode="discrete" dur="%gs" repeatCount="indefinite" values="%s" keyTimes="%s"/>`,
		c.dur, strings.Join(xs, ";"), keys)
	fmt.Fprintf(b, `<line y1="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f"/>`, bandY-2*s, bandY+bandH+5*s, o.Cursor, 1.5*s)
	fmt.Fprintf(b, `<path d="M%.1f,%.1fh%.1fl%.1f,%.1fz" fill="%s"/>`, -4*s, bandY-7*s, 8*s, -4*s, 5*s, o.Cursor)
	b.WriteString(`</g>`)

	// The caption: one pair of texts per frame, each shown on its frame. It is
	// the only part that has to be per frame — the words differ every time.
	fmt.Fprintf(b, `<g font-size="%.1f" fill="%s">`, 13*s, o.Text)
	for i, g := range frames {
		vis := make([]bool, n)
		vis[i] = true
		edges := 0
		for _, to := range g.Edges {
			edges += len(to)
		}
		b.WriteString(`<g`)
		writeVisibility(b, vis, c)
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f"><tspan fill="%s">%s</tspan> · %s · <tspan fill-opacity="0.75">%s</tspan></text>`,
			left, capY, o.Cursor, g.When, html.EscapeString(g.Commit), html.EscapeString(clip(g.Subject, int(w/(7.5*s))-40)))
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" text-anchor="end">%d packages · %d edges</text>`,
			right, capY, len(g.Nodes), edges)
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g>`)
}

func clip(s string, n int) string {
	n = max(n, 12)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func keyTimes(c clock) string {
	k := make([]string, len(c.start))
	for i, v := range c.start {
		k[i] = fmt.Sprintf("%.5g", v)
	}
	return strings.Join(k, ";")
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
func writeVisibility(b *strings.Builder, in []bool, c clock) {
	if len(in) == 0 {
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
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			vals = append(vals, boolStr(v))
			times = append(times, fmt.Sprintf("%.5g", c.start[i]))
		}
	}
	// keyTimes must start at 0 and end at 1.
	times[0] = "0"
	vals = append(vals, vals[len(vals)-1])
	times = append(times, "1")
	fmt.Fprintf(b, ` opacity="%s"><animate attributeName="opacity" calcMode="discrete" dur="%gs" repeatCount="indefinite" values="%s" keyTimes="%s"/>`,
		boolStr(in[0]), c.dur, strings.Join(vals, ";"), strings.Join(times, ";"))
}

func boolStr(v bool) string {
	if v {
		return "1"
	}
	return "0"
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
