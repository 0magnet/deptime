package deptime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests build a tiny repository rather than pointing at a real one, so
// they assert what this package decides and not what some checkout happens to
// contain today.

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repo builds two commits: one package, then a second that imports it, plus a
// vendored file and an external import that must both be ignored.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.22\n")
	write(t, dir, "a/a.go", "package a\n\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "first")

	write(t, dir, "b/b.go", "package b\n\nimport (\n\t\"fmt\"\n\t\"example.com/m/a\"\n\t\"github.com/other/thing\"\n)\n\nvar _ = fmt.Sprint\nvar _ = a.A\n")
	write(t, dir, "vendor/github.com/other/thing/t.go", "package thing\n\nimport \"example.com/m/a\"\n\nvar _ = a.A\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "second")
	return dir
}

func TestReadsTheGraphWithoutCheckingAnythingOut(t *testing.T) {
	dir := repo(t)
	r := &Repo{Dir: dir, Exclude: []string{"vendor/"}}
	frames, err := r.Commits(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("%d commits, want 2", len(frames))
	}
	// Oldest first: an animation of history runs forwards.
	if frames[0].Subject != "first" {
		t.Errorf("first frame is %q, want the oldest commit", frames[0].Subject)
	}
	for i := range frames {
		if err := r.Resolve(&frames[i]); err != nil {
			t.Fatalf("%s: %v", frames[i].Commit, err)
		}
	}

	if got := frames[0].Nodes; len(got) != 1 || got[0] != "a" {
		t.Errorf("the first commit holds %v, want just [a]", got)
	}
	if len(frames[0].Edges) != 0 {
		t.Errorf("the first commit has edges %v, want none", frames[0].Edges)
	}

	second := frames[1]
	if want := []string{"a", "b"}; !equal(second.Nodes, want) {
		t.Errorf("the second commit holds %v, want %v", second.Nodes, want)
	}
	if want := []string{"a"}; !equal(second.Edges["b"], want) {
		t.Errorf("b imports %v, want %v", second.Edges["b"], want)
	}
	// AN EXTERNAL IMPORT IS NOT AN EDGE. Only imports of this module's own
	// packages are, which is the one thing that can be known without
	// resolving anything.
	for from, tos := range second.Edges {
		for _, to := range tos {
			if strings.Contains(to, "github.com") {
				t.Errorf("%s → %s: an external import became an edge", from, to)
			}
		}
	}
	// And the vendored copy, which imports a, must not add one either.
	if _, ok := second.Edges["vendor/github.com/other/thing"]; ok {
		t.Error("a vendored package was read; Exclude did not apply")
	}
}

// The working tree must be untouched: this runs against checkouts other people
// are using, and walking history by checking commits out would break them.
func TestTheWorkingTreeIsNeverTouched(t *testing.T) {
	dir := repo(t)
	before := exec.Command("git", "rev-parse", "HEAD")
	before.Dir = dir
	head, err := before.Output()
	if err != nil {
		t.Fatal(err)
	}
	dirty := exec.Command("git", "status", "--porcelain")
	dirty.Dir = dir
	was, _ := dirty.Output()

	r := &Repo{Dir: dir, Exclude: []string{"vendor/"}}
	frames, _ := r.Commits(0, 1)
	for i := range frames {
		if err := r.Resolve(&frames[i]); err != nil {
			t.Fatal(err)
		}
	}

	after := exec.Command("git", "rev-parse", "HEAD")
	after.Dir = dir
	head2, _ := after.Output()
	if string(head) != string(head2) {
		t.Error("HEAD moved")
	}
	dirty2 := exec.Command("git", "status", "--porcelain")
	dirty2.Dir = dir
	now, _ := dirty2.Output()
	if string(was) != string(now) {
		t.Errorf("the working tree changed:\n%s", now)
	}
}

// THE POINT OF THE UNION is that a node's position is decided once, so every
// frame has to agree about which nodes exist — including the ones not yet
// created, which are present and invisible.
func TestTheUnionCarriesEveryFrameAndMarksWhenEachAppears(t *testing.T) {
	dir := repo(t)
	r := &Repo{Dir: dir, Exclude: []string{"vendor/"}}
	frames, _ := r.Commits(0, 1)
	for i := range frames {
		if err := r.Resolve(&frames[i]); err != nil {
			t.Fatal(err)
		}
	}
	u := Unite(frames)
	if !equal(u.Nodes, []string{"a", "b"}) {
		t.Fatalf("union holds %v, want [a b]", u.Nodes)
	}
	if got := u.NodeIn["a"]; !got[0] || !got[1] {
		t.Errorf("a is present in %v, want both frames", got)
	}
	if got := u.NodeIn["b"]; got[0] || !got[1] {
		t.Errorf("b is present in %v, want only the second frame", got)
	}
	if got := u.EdgeIn[[2]string{"b", "a"}]; got == nil || got[0] || !got[1] {
		t.Errorf("the b→a edge is present in %v, want only the second frame", got)
	}
}

// Collapse is what makes a six-hundred-package module readable, so it has to
// fold the edges too and drop the ones that became internal to a group.
func TestCollapseFoldsPackagesAndDropsInternalEdges(t *testing.T) {
	g := Graph{
		Nodes: []string{"pkg/one", "pkg/two", "cmd/x"},
		Edges: map[string][]string{
			"pkg/one": {"pkg/two"}, // inside one group: gone
			"cmd/x":   {"pkg/one"}, // between groups: kept
		},
		Files: map[string]int{"pkg/one": 1, "pkg/two": 2, "cmd/x": 3},
		Lines: map[string]int{"pkg/one": 10, "pkg/two": 20, "cmd/x": 30},
	}
	c := Collapse(g, 1)
	if !equal(c.Nodes, []string{"cmd", "pkg"}) {
		t.Errorf("folded to %v, want [cmd pkg]", c.Nodes)
	}
	if _, ok := c.Edges["pkg"]; ok {
		t.Errorf("pkg→pkg survived the fold: %v", c.Edges["pkg"])
	}
	if !equal(c.Edges["cmd"], []string{"pkg"}) {
		t.Errorf("cmd depends on %v, want [pkg]", c.Edges["cmd"])
	}
	// The counts add up rather than being lost.
	if c.Lines["pkg"] != 30 {
		t.Errorf("pkg holds %d lines, want 10+20", c.Lines["pkg"])
	}
}

// The compact encoding is the difference between a readable file and a
// megabyte of semicolons, so it has to actually be compact: an element that is
// visible for the whole animation carries no animation at all.
func TestVisibilityIsEncodedAsChangesNotFrames(t *testing.T) {
	var b strings.Builder
	always := make([]bool, 200)
	for i := range always {
		always[i] = true
	}
	writeVisibility(&b, always, 10)
	if strings.Contains(b.String(), "<animate") {
		t.Errorf("an always-visible element carries an animation: %q", b.String())
	}

	b.Reset()
	appears := make([]bool, 200)
	for i := 100; i < 200; i++ {
		appears[i] = true
	}
	writeVisibility(&b, appears, 10)
	out := b.String()
	if n := strings.Count(out, ";"); n > 4 {
		t.Errorf("a single appearance took %d separators; it should be a couple of stops, not one per frame:\n%s", n, out)
	}
	if !strings.Contains(out, `values="0;1;1"`) {
		t.Errorf("want an off-then-on ramp, got %q", out)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
