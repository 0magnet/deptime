# deptime

A Go module's internal package dependency graph, animated over its git history,
as one SVG.

```
deptime -repo ../skywire -frames 40 -every 120 -depth 2 -o skywire.svg
```

Nothing is checked out. Nothing is built. No module cache, no network, and the
working tree is never touched — which also means it is safe to point at a
checkout somebody else is using.

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
import block answers it without compiling anything:

```
skywire, 2,852 non-vendor .go files, 22.7 MB of source
  git cat-file --batch, whole commit      0.2 s
  + parse (parser.ImportsOnly)            0.14 s per commit
```

40 commits spanning 2022 to 2026 read in **5.6 seconds**, none skipped.

What this gives up is real: anything needing type information — which symbols
are used, whether an import survives in a file that no longer compiles,
generated code that was never committed — is invisible. The shape of the module
over time is not.

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

skywire, 40 commits, union of 621 packages and 3,683 edges:

| stage | |
|---|---|
| read 40 commits from git | 5.6 s |
| layout — `dot` | **4 m 51 s** |
| layout — `sfdp` | **0.5 s** |

`dot` is doing layered ranking and crossing minimization: the right picture for
a graph small enough to read as a hierarchy, and quadratic misery beyond it.
`sfdp` is multiscale force-directed and is what large graphs are for. The engine
is chosen by size unless `-engine` says otherwise — `dot` up to 150 nodes,
`sfdp` above.

`-depth` folds packages to a number of path elements, which is usually what you
want for an overview:

| view | packages | edges | engine | layout | SVG |
|---|---|---|---|---|---|
| `-depth 1` | 13 | 21 | dot | 59 ms | 20 KB |
| `-depth 2` | 175 | 895 | sfdp | 322 ms | 273 KB |
| every package | 621 | 3,683 | sfdp | 457 ms | 1,063 KB |

End to end, the full view is about **31 seconds** — of which 6 is git, half a
second is layout, and the rest is writing a megabyte of SVG.

## Size

Visibility is encoded as the moments an element *changes*, not one value per
frame. A package normally appears once and stays, so what is being said is
"invisible, then visible from here": two stops and two key times, whatever the
frame count. One value per frame costs every element the frame count in
characters, which on two thousand elements over two hundred frames is most of a
megabyte of semicolons.

## Flags

```
-repo    the git repository to read (default ".")
-frames  how many commits to sample (default 40)
-every   take every Nth commit (default 1)
-depth   fold packages to this many path elements (0 = every package)
-engine  auto, dot, sfdp, neato (default auto)
-fps     frames a second (default 4)
-o       write the SVG here
-stats   report each frame as it is read
```

## Viewing

SMIL animation runs in a browser, including inside `<img src="x.svg">`. Note
that "Copy image" puts a rasterised still on the clipboard — for the animation,
save the file.
