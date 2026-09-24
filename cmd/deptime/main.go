// Command deptime draws a Go module's internal dependency graph over its git
// history, as an animated SVG.
//
//	deptime -repo ../skywire -depth 2 -o skywire.svg
//
// Nothing is checked out and nothing is built: the graph is read from git
// blobs and the import blocks in them. See the package comment for what that
// buys and what it gives up.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/0magnet/deptime"
)

func main() {
	var (
		repo   = flag.String("repo", ".", "the git repository to read")
		n      = flag.Int("frames", 0, "at most this many frames, thinned evenly (0 = every change)")
		step   = flag.Int("every", 0, "sample every Nth commit instead of every change (0 = every change)")
		out    = flag.String("o", "", "write the animated SVG here")
		fps    = flag.Int("fps", 6, "frames a second")
		hold   = flag.Float64("hold", 3, "seconds to hold the final frame")
		dot    = flag.String("engine", "auto", "graphviz engine: auto, dot, sfdp, neato")
		stats  = flag.Bool("stats", false, "report each frame as it is read")
		depth  = flag.Int("depth", 0, "fold packages to this many path elements (0 = every package)")
		anchor = flag.Bool("anchor", true, "pin the newest frame at its own hierarchical layout, so the animation ends on the graph as it stands")
	)
	flag.Parse()

	r := &deptime.Repo{Dir: *repo, Exclude: []string{"vendor/"}}
	var fold func(deptime.Graph) deptime.Graph
	if *depth > 0 {
		fold = func(g deptime.Graph) deptime.Graph { return deptime.Collapse(g, *depth) }
	}

	t0 := time.Now()
	var frames []deptime.Graph
	var series []deptime.Sample
	var err error
	if *step > 0 {
		frames, err = sample(r, *n, *step, fold)
	} else {
		frames, series, err = r.Changes(fold)
	}
	if err != nil {
		die(err)
	}
	if len(frames) == 0 {
		die(fmt.Errorf("no commit produced a graph"))
	}
	found := len(frames)
	frames = deptime.Thin(frames, *n)
	read := time.Since(t0)
	if *stats {
		for _, g := range frames {
			fmt.Fprintf(os.Stderr, "%s %s %4d packages  %s\n", g.Commit, g.When, len(g.Nodes), g.Subject)
		}
	}

	t1 := time.Now()
	u := deptime.Unite(frames)
	var placed *deptime.Placed
	var engine string
	if *anchor {
		placed, engine, err = u.LayoutAnchored(frames[len(frames)-1], "dot", "neato")
	} else {
		placed, engine, err = u.Layout(*dot)
	}
	if err != nil {
		die(err)
	}
	laid := time.Since(t1)

	o := deptime.DefaultRender()
	o.FPS = *fps
	o.Hold = *hold
	placed.Crop(o.Pad)
	o.Pad = 0 // Crop already made the margin
	svg := deptime.Render(frames, series, u, placed, o)

	fmt.Fprintf(os.Stderr,
		"%d frames (of %d graph changes) · %d packages · %d edges in the union\n  read %s · one %s layout %s · %d KB\n",
		len(frames), found, len(u.Nodes), len(u.Edges),
		read.Round(time.Millisecond), engine, laid.Round(time.Millisecond), len(svg)/1024)

	if *out == "" {
		return
	}
	if err := os.WriteFile(*out, []byte(svg), 0o600); err != nil {
		die(err)
	}
}

// sample is the older way: every step-th commit, each tree read whole.
func sample(r *deptime.Repo, n, step int, fold func(deptime.Graph) deptime.Graph) ([]deptime.Graph, error) {
	frames, err := r.Commits(n, step)
	if err != nil {
		return nil, err
	}
	kept := frames[:0]
	for i := range frames {
		if err := r.Resolve(&frames[i]); err != nil {
			fmt.Fprintf(os.Stderr, "deptime: %s skipped: %v\n", frames[i].Commit, err)
			continue
		}
		if fold != nil {
			frames[i] = fold(frames[i])
		}
		kept = append(kept, frames[i])
	}
	return kept, nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "deptime:", err)
	os.Exit(1)
}
