// Command deptime draws a Go module's internal dependency graph over its git
// history, as an animated SVG. See the commands package for the details.
package main

import "github.com/0magnet/deptime/cmd/deptime/commands"

func main() { commands.Execute() }
