package deptime

import (
	"bufio"
	"fmt"
	"go/token"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Changes walks every commit on HEAD's first-parent line, oldest first, and
// returns the ones at which the graph — as fold sees it — differs from the
// one before. fold may be nil. The newest commit is always last, so the
// animation ends on the graph as it stands, dated today.
//
// WHY NOT RESOLVE EVERY COMMIT. Reading a whole tree costs about 140 ms on
// skywire, and its first-parent line is 3,890 commits: nine minutes, almost
// all of it re-reading files that did not change. A commit changes a handful
// of files. So the history is read as a diff — one `git log --raw` for which
// blobs each commit replaced, one `git cat-file --batch` over every distinct
// blob version — and each blob is parsed once, however many commits carry it.
// The tree is then replayed commit by commit in memory.
//
// WHY ONLY THE CHANGES. Most commits leave the package graph alone; they edit
// a function body. A frame for each of them would be a still picture held for
// most of the animation. Keeping a frame exactly where the graph moves means
// every change is shown and none is shown twice. Changes in size alone do not
// count, or every commit would be one.
//
// First parent, because that is the history as the default branch saw it: a
// merged branch arrives as one step, the merge, rather than as its commits
// interleaved by date with the mainline's.
func (r *Repo) Changes(fold func(Graph) Graph) ([]Graph, error) {
	commits, err := r.rawLog()
	if err != nil {
		return nil, err
	}
	info, err := r.parseAll(commits)
	if err != nil {
		return nil, err
	}

	files := map[string]*fileInfo{}
	mod := ""
	var out []Graph
	prev := ""
	for ci, c := range commits {
		touched := false
		for _, ch := range c.changes {
			touched = true
			if ch.path == "go.mod" {
				if ch.sha != "" {
					if m := info[ch.sha].module; m != "" {
						mod = m
					}
				}
				continue
			}
			if ch.sha == "" {
				delete(files, ch.path)
			} else {
				files[ch.path] = &info[ch.sha].fileInfo
			}
		}
		last := ci == len(commits)-1
		if !touched && !last {
			continue
		}
		g := c.Graph
		assemble(&g, mod, files)
		if len(g.Nodes) == 0 {
			continue
		}
		if fold != nil {
			g = fold(g)
		}
		sig := signature(g)
		switch {
		case sig != prev:
			out = append(out, g)
			prev = sig
		case last:
			// The graph did not move, but the animation should end on HEAD's
			// date rather than on whenever it last changed.
			out = append(out, g)
		}
	}
	return out, nil
}

// signature is a graph's shape as a string: nodes and edges, not sizes.
func signature(g Graph) string {
	var b strings.Builder
	for _, n := range g.Nodes {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	b.WriteByte(0)
	from := make([]string, 0, len(g.Edges))
	for f := range g.Edges {
		from = append(from, f)
	}
	sort.Strings(from)
	for _, f := range from {
		b.WriteString(f)
		b.WriteByte('>')
		b.WriteString(strings.Join(g.Edges[f], ","))
		b.WriteByte('\n')
	}
	return b.String()
}

type change struct {
	path string
	sha  string // "" when the file was deleted
}

type logCommit struct {
	Graph
	changes []change // Go files and the root go.mod only
}

// rawLog lists the first-parent history, oldest first, with the Go files and
// go.mod each commit replaced relative to its first parent.
func (r *Repo) rawLog() ([]logCommit, error) {
	out, err := r.git("log", "--first-parent", "--reverse", "--root",
		"--diff-merges=first-parent", "--raw", "--no-renames", "--no-abbrev",
		"--format=\x1e%H\x1f%ad\x1f%s", "--date=short", "HEAD")
	if err != nil {
		return nil, err
	}
	var commits []logCommit
	for _, rec := range strings.Split(out, "\x1e")[1:] {
		head, body, _ := strings.Cut(rec, "\n")
		f := strings.SplitN(head, "\x1f", 3)
		if len(f) != 3 {
			continue
		}
		c := logCommit{Graph: Graph{Commit: short(f[0]), When: f[1], Subject: f[2]}}
		for _, line := range strings.Split(body, "\n") {
			// :100644 100644 <old> <new> M\t<path>
			meta, p, ok := strings.Cut(line, "\t")
			if !ok || !strings.HasPrefix(meta, ":") {
				continue
			}
			if p != "go.mod" && (!strings.HasSuffix(p, ".go") || r.excluded(p)) {
				continue
			}
			fields := strings.Fields(meta)
			if len(fields) < 5 {
				continue
			}
			sha := fields[3]
			if fields[4] == "D" {
				sha = ""
			}
			c.changes = append(c.changes, change{path: p, sha: sha})
		}
		commits = append(commits, c)
	}
	return commits, nil
}

type blobInfo struct {
	fileInfo
	module string // for a go.mod
}

// parseAll reads every distinct blob the history mentions, in one git process.
func (r *Repo) parseAll(commits []logCommit) (map[string]*blobInfo, error) {
	var refs []blobRef
	seen := map[string]bool{}
	for _, c := range commits {
		for _, ch := range c.changes {
			if ch.sha != "" && !seen[ch.sha] {
				seen[ch.sha] = true
				refs = append(refs, blobRef{ch.sha, ch.path})
			}
		}
	}
	info := make(map[string]*blobInfo, len(refs))
	fset := token.NewFileSet()
	err := r.eachBlob(refs, func(f blobRef, src []byte) {
		bi := &blobInfo{}
		if strings.HasSuffix(f.path, ".go") {
			bi.fileInfo = parseFile(fset, f.path, src)
		} else {
			bi.module = modulePath(src)
		}
		info[f.sha] = bi
	})
	return info, err
}

// eachBlob streams blobs through `git cat-file --batch`, handing each to fn
// and keeping none of them: a whole history's source does not need to be in
// memory at once, only what was learned from it.
func (r *Repo) eachBlob(files []blobRef, fn func(blobRef, []byte)) error {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = r.Dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		// Write errors show up on the read side, which can say which object.
		w := bufio.NewWriter(in)
		for _, f := range files {
			if _, err := w.WriteString(f.sha + "\n"); err != nil {
				break
			}
		}
		_ = w.Flush()
		_ = in.Close()
	}()

	br := bufio.NewReaderSize(out, 1<<20)
	var buf []byte
	for _, f := range files {
		header, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("cat-file: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return fmt.Errorf("cat-file: unexpected header %q", header)
		}
		n, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("cat-file: bad size in %q", header)
		}
		if cap(buf) < n {
			buf = make([]byte, n)
		}
		buf = buf[:n]
		if _, err := io.ReadFull(br, buf); err != nil {
			return err
		}
		if _, err := br.Discard(1); err != nil {
			return err
		}
		fn(f, buf)
	}
	return cmd.Wait()
}

// Thin keeps at most n frames, spread evenly and always including the first
// and the last. n <= 0 or n >= len(frames) keeps them all.
//
// A dropped frame's changes are not lost from the picture — the next kept
// frame carries the graph as it then stood — only the in-between states are.
// A package that came and went entirely between two kept frames is the one
// thing that vanishes.
func Thin(frames []Graph, n int) []Graph {
	if n <= 0 || n >= len(frames) {
		return frames
	}
	if n == 1 {
		return frames[len(frames)-1:]
	}
	out := make([]Graph, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, frames[i*(len(frames)-1)/(n-1)])
	}
	return out
}

// short abbreviates a commit hash. `%h` cannot do it here: --no-abbrev, which
// the raw diff needs for whole blob names, lengthens %h to the full hash too.
func short(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}
