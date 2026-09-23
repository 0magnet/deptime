// Package deptime reads a Go module's internal package dependency graph out of
// git history, without checking anything out and without building anything.
//
// WHY NOT goda. goda answers this question beautifully for a working tree: it
// loads the packages properly, which means resolving the module graph, which
// means a module cache and usually a network. Asked the same question about a
// commit from two years ago it has to check that commit out and resolve the
// dependencies it had then — tens of seconds when it works, and it often does
// not, because the versions it wants have moved or gone. Over a few hundred
// commits that is hours and a pile of failures.
//
// The dependency graph BETWEEN A MODULE'S OWN PACKAGES needs none of that. An
// import of another package in the same module is written in the file, the
// package a file belongs to is the directory it is in, and both are visible
// without compiling anything or knowing what any external import resolves to.
// Reading blobs straight out of git and parsing only the import block answers
// it in a third of a second for a repository the size of skywire — measured at
// 22.7 MB of Go source across 2,852 files.
//
// What this gives up is real: anything requiring type information (which
// symbols are used, whether an import is only in tests that no longer build,
// generated files that were not committed) is invisible here. The shape of the
// module over time is not.
package deptime

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Graph is one commit's internal package dependency graph.
type Graph struct {
	Commit  string              // short hash
	When    string              // author date, YYYY-MM-DD
	Subject string              // commit subject, for captions
	Nodes   []string            // import paths relative to the module root, "." for the root
	Edges   map[string][]string // from → to, both as Nodes entries
	Files   map[string]int      // node → how many .go files it holds
	Lines   map[string]int      // node → how many lines of Go it holds
}

// Repo is a git repository read through plumbing, so that nothing in the
// working tree is touched. That is not only tidiness: the checkout this was
// written against is shared with other work, and switching its branch to walk
// history would have broken whatever else was using it.
type Repo struct {
	Dir     string
	Exclude []string // path prefixes to ignore, e.g. "vendor/"
}

// Commits lists commits on HEAD, newest first, limited to at most n and taking
// every step-th one.
func (r *Repo) Commits(n, step int) ([]Graph, error) {
	if step < 1 {
		step = 1
	}
	out, err := r.git("log", "--format=%h\x1f%ad\x1f%s", "--date=short", "HEAD")
	if err != nil {
		return nil, err
	}
	var all []Graph
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.SplitN(line, "\x1f", 3)
		if len(f) != 3 {
			continue
		}
		all = append(all, Graph{Commit: f[0], When: f[1], Subject: f[2]})
	}
	var picked []Graph
	for i := 0; i < len(all); i += step {
		picked = append(picked, all[i])
		if n > 0 && len(picked) >= n {
			break
		}
	}
	// Oldest first: an animation of history runs forwards.
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked, nil
}

// ModulePath is the module's own import path at a commit, from its go.mod.
// Without it there is no way to tell an internal import from an external one.
func (r *Repo) ModulePath(commit string) (string, error) {
	out, err := r.git("show", commit+":go.mod")
	if err != nil {
		return "", fmt.Errorf("%s has no go.mod: %w", commit, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(s), nil
		}
	}
	return "", fmt.Errorf("%s: go.mod declares no module path", commit)
}

// Resolve fills in a commit's graph.
func (r *Repo) Resolve(g *Graph) error {
	mod, err := r.ModulePath(g.Commit)
	if err != nil {
		return err
	}
	files, err := r.goFiles(g.Commit)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	contents, err := r.blobs(files)
	if err != nil {
		return err
	}

	edges := map[string]map[string]bool{}
	g.Files = map[string]int{}
	g.Lines = map[string]int{}
	fset := token.NewFileSet()
	for i, f := range files {
		src := contents[i]
		pkg := path.Dir(f.path)
		if pkg == "." {
			pkg = "."
		}
		g.Files[pkg]++
		g.Lines[pkg] += 1 + strings.Count(string(src), "\n")
		// ImportsOnly: the parser stops at the end of the import block, which
		// is the whole reason this is fast enough to run over history.
		af, err := parser.ParseFile(fset, f.path, src, parser.ImportsOnly)
		if err != nil {
			continue // a file that did not parse then is not an edge now
		}
		for _, im := range af.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				continue
			}
			rel, ok := internalTo(mod, p)
			if !ok {
				continue
			}
			if rel == pkg {
				continue
			}
			if edges[pkg] == nil {
				edges[pkg] = map[string]bool{}
			}
			edges[pkg][rel] = true
		}
	}

	seen := map[string]bool{}
	for p := range g.Files {
		seen[p] = true
	}
	for from, tos := range edges {
		seen[from] = true
		for to := range tos {
			seen[to] = true
		}
	}
	g.Nodes = make([]string, 0, len(seen))
	for p := range seen {
		g.Nodes = append(g.Nodes, p)
	}
	sort.Strings(g.Nodes)
	g.Edges = map[string][]string{}
	for from, tos := range edges {
		list := make([]string, 0, len(tos))
		for to := range tos {
			list = append(list, to)
		}
		sort.Strings(list)
		g.Edges[from] = list
	}
	return nil
}

// internalTo reports whether an import belongs to this module, and what it is
// called relative to the module root.
func internalTo(mod, imp string) (string, bool) {
	if imp == mod {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(imp, mod+"/"); ok {
		return rest, true
	}
	return "", false
}

type blobRef struct {
	sha  string
	path string
}

func (r *Repo) goFiles(commit string) ([]blobRef, error) {
	out, err := r.git("ls-tree", "-r", "--format=%(objectname) %(path)", commit)
	if err != nil {
		return nil, err
	}
	var files []blobRef
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		sha, p, ok := strings.Cut(line, " ")
		if !ok || !strings.HasSuffix(p, ".go") {
			continue
		}
		if r.excluded(p) {
			continue
		}
		files = append(files, blobRef{sha, p})
	}
	return files, nil
}

func (r *Repo) excluded(p string) bool {
	for _, e := range r.Exclude {
		if strings.HasPrefix(p, e) {
			return true
		}
	}
	return false
}

// blobs reads every file's contents in ONE git process.
//
// `git cat-file --batch` takes a list of object names on stdin and streams the
// contents back, so a commit's whole source costs one fork rather than one per
// file. On skywire that is 22.7 MB in about two tenths of a second; running
// `git show` per file would be 2,852 processes.
func (r *Repo) blobs(files []blobRef) ([][]byte, error) {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = r.Dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		// Errors here are not reported: a short or broken stream shows up on
		// the read side below, which is where it can say which object failed.
		w := bufio.NewWriter(in)
		for _, f := range files {
			if _, err := w.WriteString(f.sha + "\n"); err != nil {
				break
			}
		}
		if err := w.Flush(); err != nil {
			_ = err
		}
		_ = in.Close()
	}()

	br := bufio.NewReaderSize(out, 1<<20)
	contents := make([][]byte, 0, len(files))
	for range files {
		header, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file: %w", err)
		}
		fields := strings.Fields(strings.TrimSpace(header))
		if len(fields) != 3 {
			return nil, fmt.Errorf("cat-file: unexpected header %q", header)
		}
		n, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("cat-file: bad size in %q", header)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		if _, err := br.Discard(1); err != nil { // the trailing newline
			return nil, err
		}
		contents = append(contents, buf)
	}
	return contents, cmd.Wait()
}

func (r *Repo) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
