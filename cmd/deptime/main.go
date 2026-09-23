// Command deptime draws a Go module's internal dependency graph over its git
// history, as an animated SVG.
//
//	deptime -repo ../skywire -frames 60 -every 100 -o skywire.svg
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
		repo  = flag.String("repo", ".", "the git repository to read")
		n     = flag.Int("frames", 40, "how many commits to sample")
		step  = flag.Int("every", 1, "take every Nth commit")
		out   = flag.String("o", "", "write the animated SVG here")
		fps   = flag.Int("fps", 4, "frames a second")
		dot   = flag.String("engine", "auto", "graphviz engine: auto, dot, sfdp, neato")
		stats = flag.Bool("stats", false, "report each frame as it is read")
		depth = flag.Int("depth", 0, "fold packages to this many path elements (0 = every package)")
	)
	flag.Parse()

	r := &deptime.Repo{Dir: *repo, Exclude: []string{"vendor/"}}
	frames, err := r.Commits(*n, *step)
	if err != nil {
		die(err)
	}
	if len(frames) == 0 {
		die(fmt.Errorf("no commits"))
	}

	t0 := time.Now()
	kept := frames[:0]
	for i := range frames {
		if err := r.Resolve(&frames[i]); err != nil {
			fmt.Fprintf(os.Stderr, "deptime: %s skipped: %v\n", frames[i].Commit, err)
			continue
		}
		if *stats {
			fmt.Fprintf(os.Stderr, "%s %s %4d packages\n", frames[i].Commit, frames[i].When, len(frames[i].Nodes))
		}
		kept = append(kept, frames[i])
	}
	frames = kept
	if len(frames) == 0 {
		die(fmt.Errorf("no commit produced a graph"))
	}
	read := time.Since(t0)

	if *depth > 0 {
		for i := range frames {
			frames[i] = deptime.Collapse(frames[i], *depth)
		}
	}

	t1 := time.Now()
	u := deptime.Unite(frames)
	placed, engine, err := u.Layout(*dot)
	if err != nil {
		die(err)
	}
	laid := time.Since(t1)

	o := deptime.DefaultRender()
	o.FPS = *fps
	placed.Crop(o.Pad)
	o.Pad = 0 // Crop already made the margin
	svg := deptime.Render(frames, u, placed, o)

	fmt.Fprintf(os.Stderr,
		"%d frames · %d packages · %d edges in the union\n  read %s (%.0fms a commit) · one %s layout %s · %d KB\n",
		len(frames), len(u.Nodes), len(u.Edges),
		read.Round(time.Millisecond), float64(read.Milliseconds())/float64(len(frames)),
		engine, laid.Round(time.Millisecond), len(svg)/1024)

	if *out == "" {
		return
	}
	if err := os.WriteFile(*out, []byte(svg), 0o600); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "deptime:", err)
	os.Exit(1)
}
