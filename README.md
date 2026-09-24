# deptime

A Go module's internal package dependency graph, animated over its git history,
as one SVG — with a timeline across the top showing where in the history the
graph on screen comes from.

```
deptime -repo ../skywire -depth 2 -o skywire.svg
```

Nothing is checked out. Nothing is built. No module cache, no network, and the
working tree is never touched — which also means it is safe to point at a
checkout somebody else is using.

## Every change, not a sample

By default there is one frame for every commit on the first-parent line at
which the graph changed: a package added or removed, an import between two of
them added or dropped. Commits that only edit function bodies are not frames —
they would be the same picture held for most of the animation. Changes in size
alone do not count either, or every commit would be one.

On skywire that is 3,890 first-parent commits since 2019, of which **319**
change the graph at `-depth 2` and **575** at full package depth. All of them
are in the animation.

`-frames N` thins to at most N, evenly, keeping the first and last; `-every N`
is the old behavior of sampling every Nth commit.

First parent, because that is the history as the default branch saw it: a
merged branch arrives as one step, the merge.

## The timeline

The header across the top is the calendar. In it:

- two strips on the same time axis: the **package count**, and **lines of Go
  code** — the second at every commit that touched Go, not only the frames,
  since size moves with nearly every commit and the graph with few. Each is
  bright up to the frame on screen and dim ahead of it, and labeled with the
  peak it is drawn against;
- a tick under the strips for every frame — where the ticks are dense, the
  structure was being worked on;
- year lines;
- the cursor, on the frame on screen, with its date, commit, subject, package
  and edge counts above, and under it **gocloc's row for Go** at that commit:
  files, blank, comment, code.

Frames get equal time on screen, not equal time on the calendar, so the cursor
races across a quiet year and crawls through a busy month. A package is lit on
the frame it arrives. The last frame holds for `-hold` seconds (default 3) so
the graph as it stands stays up before the loop starts again.

### Lines of code, for free

Every Go blob in the history is already read once for its imports, so its lines
are counted in the same pass, the way gocloc counts them. It adds nothing
measurable to the run. Only Go: counting other languages would mean reading
every other blob in the history as well.

Checked against gocloc on skywire's HEAD, 2,884 files: files and blank lines
agree exactly, and code and comment agree on all but two files. Both embed
shell scripts in string literals, where gocloc reads `"${1}"/*/` as the start
of a block comment and counts a thousand lines of code as comment.

## Why not just run goda per commit

[goda](https://github.com/loov/goda) answers this question properly for a
working tree: it loads the packages, which means resolving the module graph,
which means a module cache and usually a network. Asked the same question about
a commit from two years ago, it has to check that commit out and resolve the
dependencies *it* had then — tens of seconds when it works, and often it does
not, because the versions it wants have moved or gone.

The graph **between a module's own packages** needs none of that. An import of
a sibling package is written in the file; the package a file belongs to is the
directory it sits in. Reading blobs straight out of git and parsing only the
import block answers it without compiling anything.

Reading every commit's whole tree would still be slow — about 140 ms a commit
on skywire, nine minutes for its history, nearly all of it re-reading files
that did not change. So history is read as a diff: one `git log --raw` for
which blobs each commit replaced, one `git cat-file --batch` over every
distinct blob version, each parsed once however many commits carry it, and the
tree replayed in memory. Skywire's whole history reads in **12 to 15 seconds**.

What this gives up is real: anything needing type information — which symbols
are used, whether an import survives in a file that no longer compiles,
generated code that was never committed — is invisible. The shape of the
module over time is not.

## Anchoring on the newest frame

A union layout is stable, which is the point, but it is a layout of a graph
nobody ever had: on skywire, 305 of the 705 packages in the union — 43% — existed
at some point and are gone at HEAD, and they pull the picture around. The final
frame would not look like the dependency graph of the project as it stands,
which is the one picture a reader already knows.

Laying out only the newest state and walking backwards is the obvious fix and
throws those 305 away: much of the history would never appear.

So `-anchor` (on by default) does both. The newest frame is laid out on its own
with `dot`, its packages are pinned at those coordinates, and the departed ones
are placed around them by a force pass that cannot move anything pinned. The
animation ends on the graph as it is, and everything else grew into it.

## The layout is the whole problem

Graph layout is not stable under small changes. Add one package to a
400-package graph, run `dot` again, and half the other packages move, because
the layout is a global optimization with no term for *stay where you were*. An
animation made frame by frame that way is a flicker of unrelated pictures.

So the union of every frame is laid out **once**. Every node keeps that
position for the life of the animation, and a frame says only which nodes and
edges are *visible*. A package added in 2024 is already sitting in its final
place in the 2022 frame, unseen, and simply appears.

## Cost, measured

skywire, every graph change on the first-parent line, 2019–2026:

| view | frames | packages | edges | read | layout | SVG |
|---|---|---|---|---|---|---|
| `-depth 2` | 319 | 201 | 1,071 | 15 s | 19 s | 921 KB |
| every package | 575 | 705 | 4,150 | 8 s | 5 m 49 s | 3.9 MB |

The full view's layout is `dot` over the 400 packages at HEAD, for the
anchor. `dot` does layered ranking and crossing minimization, the right picture
for a hierarchy and quadratic beyond a few hundred nodes. `-anchor=false` lays
out the union with `sfdp` instead, in about half a second, at the price of an
ending that does not look like HEAD. Without the anchor the engine is chosen by
size unless `-engine` says otherwise — `dot` up to 150 nodes, `sfdp` above.

`-depth` folds packages to a number of path elements, which is usually what you
want for an overview.

## Size

Visibility is encoded as the moments an element *changes*, not one value per
frame. A package normally appears once and stays, so what is being said is
"invisible, then visible from here": two stops and two key times, whatever the
frame count. The per-frame captions and the timeline cursor are the only parts
that grow with the number of frames.

## Flags

```
-repo    the git repository to read (default ".")
-frames  at most this many frames, thinned evenly (default 0 = every change)
-every   sample every Nth commit instead of every change (default 0)
-depth   fold packages to this many path elements (0 = every package)
-engine  auto, dot, sfdp, neato (default auto; ignored when -anchor is on)
-anchor  pin the newest frame at its own dot layout (default true)
-fps     frames a second (default 6)
-hold    seconds to hold the final frame (default 3)
-o       write the SVG here
-stats   list each frame
```

## Viewing

SMIL animation runs in a browser, including inside `<img src="x.svg">`. Note
that "Copy image" puts a rasterized still on the clipboard — for the animation,
save the file.
