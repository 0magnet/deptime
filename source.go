package deptime

import (
	"bytes"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// fileInfo is everything the graph needs from one Go file: its import paths
// and its size. It is keyed by blob, not by path, because a blob's contents
// never change — a file that sits untouched through a thousand commits is
// parsed once, and so is a file renamed into another package.
type fileInfo struct {
	imports []string // as written, internal or not
	lines   int
}

// parseFile reads a file's import block. ImportsOnly makes the parser stop at
// the end of it, which is the whole reason this is fast enough to run over
// history. A file that does not parse contributes its size and no edges.
func parseFile(fset *token.FileSet, name string, src []byte) fileInfo {
	fi := fileInfo{lines: 1 + bytes.Count(src, []byte("\n"))}
	af, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
	if err != nil {
		return fi
	}
	for _, im := range af.Imports {
		if p, err := strconv.Unquote(im.Path.Value); err == nil {
			fi.imports = append(fi.imports, p)
		}
	}
	return fi
}

// modulePath reads the module line out of a go.mod.
func modulePath(src []byte) string {
	for _, line := range strings.Split(string(src), "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(s), `"`)
		}
	}
	return ""
}

// assemble builds a commit's graph from its files, path → contents summary.
func assemble(g *Graph, mod string, files map[string]*fileInfo) {
	edges := map[string]map[string]bool{}
	g.Files = map[string]int{}
	g.Lines = map[string]int{}
	for p, fi := range files {
		pkg := path.Dir(p)
		g.Files[pkg]++
		g.Lines[pkg] += fi.lines
		for _, imp := range fi.imports {
			rel, ok := internalTo(mod, imp)
			if !ok || rel == pkg {
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
}

// Resolve fills in one commit's graph by reading its whole tree.
func (r *Repo) Resolve(g *Graph) error {
	mod, err := r.ModulePath(g.Commit)
	if err != nil {
		return err
	}
	refs, err := r.goFiles(g.Commit)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	contents, err := r.blobs(refs)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	files := make(map[string]*fileInfo, len(refs))
	for i, f := range refs {
		fi := parseFile(fset, f.path, contents[i])
		files[f.path] = &fi
	}
	assemble(g, mod, files)
	return nil
}

// internalTo reports whether an import belongs to this module, and what it is
// called relative to the module root.
func internalTo(mod, imp string) (string, bool) {
	if mod == "" {
		return "", false
	}
	if imp == mod {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(imp, mod+"/"); ok {
		return rest, true
	}
	return "", false
}
