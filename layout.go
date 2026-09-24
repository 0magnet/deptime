package deptime

import (
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Laying the graph out ONCE, over the union of every frame.
//
// This is the whole difficulty of animating a dependency graph, and the reason
// it is not simply "render each commit and string them together". Graph layout
// is not stable under small changes: adding one package to a 400-package graph
// and running dot again moves half the other packages, because the layout is a
// global optimization and there is no term in it for "stay where you were". An
// animation made that way is a flicker of unrelated pictures — nothing can be
// followed, which is the only thing an animation is for.
//
// So the union of all frames is laid out once, every node and edge keeps that
// position for the life of the animation, and a frame is a statement about
// which of them are VISIBLE. A package added in 2024 is already sitting in its
// final place in the 2022 frame, unseen, and simply appears. That is the same
// trick the still and animated pictures elsewhere use — fit to everything,
// draw a subset — and it is what makes the growth legible: things enter, and
// the shape around them holds still.

// Union is every node and edge that appears in any frame, with the frames each
// is present in.
type Union struct {
	Nodes []string
	Edges [][2]string
	// NodeIn[node] and EdgeIn[edge] are per-frame presence.
	NodeIn map[string][]bool
	EdgeIn map[[2]string][]bool
	// Weight is the largest a node ever gets, in Go lines, for sizing.
	Weight map[string]int
}

// Unite collects the frames into one graph.
func Unite(frames []Graph) *Union {
	u := &Union{
		NodeIn: map[string][]bool{},
		EdgeIn: map[[2]string][]bool{},
		Weight: map[string]int{},
	}
	n := len(frames)
	for i, g := range frames {
		for _, node := range g.Nodes {
			if u.NodeIn[node] == nil {
				u.NodeIn[node] = make([]bool, n)
			}
			u.NodeIn[node][i] = true
			if w := g.Lines[node]; w > u.Weight[node] {
				u.Weight[node] = w
			}
		}
		for from, tos := range g.Edges {
			for _, to := range tos {
				e := [2]string{from, to}
				if u.EdgeIn[e] == nil {
					u.EdgeIn[e] = make([]bool, n)
				}
				u.EdgeIn[e][i] = true
			}
		}
	}
	for node := range u.NodeIn {
		u.Nodes = append(u.Nodes, node)
	}
	sort.Strings(u.Nodes)
	for e := range u.EdgeIn {
		u.Edges = append(u.Edges, e)
	}
	sort.Slice(u.Edges, func(i, j int) bool {
		if u.Edges[i][0] != u.Edges[j][0] {
			return u.Edges[i][0] < u.Edges[j][0]
		}
		return u.Edges[i][1] < u.Edges[j][1]
	})
	return u
}

// Placed is the union after graphviz has decided where everything goes.
type Placed struct {
	W, H float64
	Node map[string]Box
	Edge map[[2]string][][2]float64 // the spline's control points
}

// Box is a laid-out node, in SVG user units with y already flipped.
type Box struct{ X, Y, W, H float64 }

// Layout runs graphviz over the union and reads the positions back.
//
// -Tplain rather than -Tsvg. Graphviz can draw the SVG itself and then there
// is no way in: the animation needs every node and edge to be an element it
// can address, with an id it chose, carrying its own visibility. The plain
// format is positions and splines in six lines of grammar, which is a
// half-page parser against a full SVG one.
// ENGINE MATTERS MORE THAN ANYTHING ELSE HERE. Measured on skywire's union —
// 621 packages, 3,683 edges — dot took 4 minutes 51 seconds and sfdp took half
// a second, for the same 621 nodes placed. dot is doing layered ranking and
// crossing minimization, which is the right picture for a graph small enough to
// read as a hierarchy and quadratic misery beyond it; sfdp is multiscale
// force-directed and is what large graphs are for.
//
// So the engine is chosen by size unless asked. The threshold is where dot is
// still a few seconds, and below it the hierarchy is worth having: a module
// with fifty packages reads as layers, and one with six hundred reads as
// clusters whatever you do to it.
const hierarchyLimit = 150

// Layout runs graphviz over the union and reads the positions back.
func (u *Union) Layout(engine string) (*Placed, string, error) {
	if engine == "" || engine == "auto" {
		engine = "dot"
		if len(u.Nodes) > hierarchyLimit {
			engine = "sfdp"
		}
	}
	cmd := exec.Command(engine, "-Tplain")
	cmd.Stdin = strings.NewReader(u.DOT())
	out, err := cmd.Output()
	if err != nil {
		return nil, engine, fmt.Errorf("%s -Tplain: %w", engine, err)
	}
	p, err := parsePlain(string(out))
	return p, engine, err
}

// nodeWidth keeps the boxes roughly the width of their labels, so the layout
// leaves room for text that is drawn later rather than packing them as if the
// names were all the same length.
func nodeWidth(name string) float64 {
	w := 0.10 * float64(len(shortName(name)))
	if w < 0.5 {
		w = 0.5
	}
	return w
}

// shortName is what a node is labeled with: the last two path elements, which
// is what distinguishes sibling packages without printing the whole tree on
// every box.
func shortName(p string) string {
	if p == "." {
		return "(root)"
	}
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

const plainScale = 72 // graphviz plain reports inches; SVG wants points

func parsePlain(s string) (*Placed, error) {
	p := &Placed{Node: map[string]Box{}, Edge: map[[2]string][][2]float64{}}
	for _, line := range strings.Split(s, "\n") {
		f := splitPlain(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "graph":
			if len(f) < 4 {
				continue
			}
			w, _ := strconv.ParseFloat(f[2], 64)
			h, _ := strconv.ParseFloat(f[3], 64)
			p.W, p.H = w*plainScale, h*plainScale
		case "node":
			if len(f) < 6 {
				continue
			}
			x, _ := strconv.ParseFloat(f[2], 64)
			y, _ := strconv.ParseFloat(f[3], 64)
			w, _ := strconv.ParseFloat(f[4], 64)
			h, _ := strconv.ParseFloat(f[5], 64)
			p.Node[f[1]] = Box{
				X: x * plainScale,
				// Graphviz measures from the bottom left, SVG from the top.
				Y: p.H - y*plainScale,
				W: w * plainScale,
				H: h * plainScale,
			}
		case "edge":
			if len(f) < 4 {
				continue
			}
			n, err := strconv.Atoi(f[3])
			if err != nil || len(f) < 4+2*n {
				continue
			}
			pts := make([][2]float64, 0, n)
			for i := 0; i < n; i++ {
				x, _ := strconv.ParseFloat(f[4+2*i], 64)
				y, _ := strconv.ParseFloat(f[5+2*i], 64)
				pts = append(pts, [2]float64{x * plainScale, p.H - y*plainScale})
			}
			p.Edge[[2]string{f[1], f[2]}] = pts
		}
	}
	if p.W == 0 || p.H == 0 {
		return nil, fmt.Errorf("graphviz returned no graph size")
	}
	return p, nil
}

// splitPlain splits a plain-format line, honoring the quoting graphviz uses
// for names with spaces or slashes in them.
func splitPlain(line string) []string {
	var out []string
	var cur strings.Builder
	inQ, esc := false, false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '"':
			inQ = !inQ
		case r == ' ' && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// DOT is the union as a graphviz source file, for trying engines by hand.
func (u *Union) DOT() string {
	var b strings.Builder
	b.WriteString("digraph G {\n  rankdir=LR;\n  node [shape=box];\n")
	for _, n := range u.Nodes {
		fmt.Fprintf(&b, "  %q [width=%.3f,height=0.3];\n", n, nodeWidth(n))
	}
	for _, e := range u.Edges {
		fmt.Fprintf(&b, "  %q -> %q;\n", e[0], e[1])
	}
	b.WriteString("}\n")
	return b.String()
}

// Crop moves the content to the origin and reports the size it actually
// occupies.
//
// Graphviz's reported canvas is not the content's bounding box — sfdp in
// particular returns a sheet much larger than the nodes it placed, and the
// picture came out in one corner of a mostly empty rectangle. Measuring the
// nodes and edges that exist is the only reliable answer, and it costs one
// pass over data already in memory.
func (p *Placed) Crop(pad float64) {
	if len(p.Node) == 0 {
		return
	}
	loX, loY := math.Inf(1), math.Inf(1)
	hiX, hiY := math.Inf(-1), math.Inf(-1)
	grow := func(x, y float64) {
		loX, hiX = math.Min(loX, x), math.Max(hiX, x)
		loY, hiY = math.Min(loY, y), math.Max(hiY, y)
	}
	for _, b := range p.Node {
		grow(b.X-b.W/2, b.Y-b.H/2)
		grow(b.X+b.W/2, b.Y+b.H/2)
	}
	for _, pts := range p.Edge {
		for _, pt := range pts {
			grow(pt[0], pt[1])
		}
	}
	dx, dy := pad-loX, pad-loY
	for k, b := range p.Node {
		b.X += dx
		b.Y += dy
		p.Node[k] = b
	}
	for k, pts := range p.Edge {
		for i := range pts {
			pts[i][0] += dx
			pts[i][1] += dy
		}
		p.Edge[k] = pts
	}
	p.W, p.H = hiX-loX+2*pad, hiY-loY+2*pad
}

// Collapse folds packages to at most depth path elements, so that a module
// with six hundred leaves reads as the couple of dozen groups it is organized
// into. Edges inside a group disappear; edges between groups survive.
//
// The whole-graph view answers "what is in here"; this answers "what depends
// on what", which over four years of history is the question worth animating.
func Collapse(g Graph, depth int) Graph {
	if depth <= 0 {
		return g
	}
	fold := func(p string) string {
		if p == "." {
			return "."
		}
		parts := strings.Split(p, "/")
		if len(parts) > depth {
			parts = parts[:depth]
		}
		return strings.Join(parts, "/")
	}
	out := Graph{Commit: g.Commit, When: g.When, Subject: g.Subject,
		Edges: map[string][]string{}, Files: map[string]int{}, Lines: map[string]int{}}
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		f := fold(n)
		seen[f] = true
		out.Files[f] += g.Files[n]
		out.Lines[f] += g.Lines[n]
	}
	edges := map[string]map[string]bool{}
	for from, tos := range g.Edges {
		ff := fold(from)
		for _, to := range tos {
			ft := fold(to)
			if ff == ft {
				continue
			}
			seen[ff], seen[ft] = true, true
			if edges[ff] == nil {
				edges[ff] = map[string]bool{}
			}
			edges[ff][ft] = true
		}
	}
	for n := range seen {
		out.Nodes = append(out.Nodes, n)
	}
	sort.Strings(out.Nodes)
	for from, tos := range edges {
		list := make([]string, 0, len(tos))
		for to := range tos {
			list = append(list, to)
		}
		sort.Strings(list)
		out.Edges[from] = list
	}
	return out
}

// LayoutAnchored lays the union out so that the LAST frame keeps the positions
// a plain hierarchical layout of that frame alone would give it.
//
// The union layout is stable — nothing moves between frames, which is the
// point — but it is a layout of a graph nobody ever had: on skywire, 232 of
// the 632 packages in the union (37%) existed at some point and are gone at
// HEAD, and they pull the picture around. So the final frame does not look
// like the dependency graph of the project as it stands, which is the one
// picture a reader already knows.
//
// Laying out only the final state instead, and going backwards, is the obvious
// fix and throws those 232 away: a third of the history would never appear.
//
// So both. The final frame is laid out on its own with dot, its packages are
// PINNED at those coordinates, and the departed ones are placed around them by
// a force pass that cannot move anything pinned. The end of the animation is
// the graph as it is; everything else grew into it.
func (u *Union) LayoutAnchored(last Graph, hierarchy, free string) (*Placed, string, error) {
	if hierarchy == "" {
		hierarchy = "dot"
	}
	if free == "" {
		free = "neato"
	}
	// Step one: the final state alone, as a hierarchy.
	anchor := Unite([]Graph{last})
	fixed, err := runGraphviz(hierarchy, anchor.DOT())
	if err != nil {
		return nil, hierarchy, err
	}

	// Step two: the union, with those positions nailed down. The "!" is
	// graphviz's own way of saying a node may not be moved.
	var b strings.Builder
	b.WriteString("digraph G {\n  rankdir=LR;\n  node [shape=box];\n  overlap=false;\n  splines=true;\n")
	for _, n := range u.Nodes {
		if p, ok := fixed.Node[n]; ok {
			fmt.Fprintf(&b, "  %q [width=%.3f,height=0.3,pos=%q];\n", n, nodeWidth(n),
				fmt.Sprintf("%.2f,%.2f!", p.X, fixed.H-p.Y))
			continue
		}
		fmt.Fprintf(&b, "  %q [width=%.3f,height=0.3];\n", n, nodeWidth(n))
	}
	for _, e := range u.Edges {
		fmt.Fprintf(&b, "  %q -> %q;\n", e[0], e[1])
	}
	b.WriteString("}\n")

	p, err := runGraphviz(free, b.String())
	return p, hierarchy + "+" + free, err
}

func runGraphviz(engine, dot string) (*Placed, error) {
	cmd := exec.Command(engine, "-Tplain")
	cmd.Stdin = strings.NewReader(dot)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s -Tplain: %w", engine, err)
	}
	return parsePlain(string(out))
}
