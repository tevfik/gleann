//go:build treesitter

package main

// graphFlagSets maps each `gleann graph` subcommand to its flags. Every
// graph subcommand builds its Config with getConfig, hence configFlags.
// graph toc skips a "--config <value>" pair when it looks for the document
// path, so that spelling is accepted too.
var graphFlagSets = map[string]flagSet{
	"deps":        configFlags.union(flagSet{"--index": true}),
	"callers":     configFlags.union(flagSet{"--index": true}),
	"explain":     configFlags.union(flagSet{"--index": true}),
	"path":        configFlags.union(flagSet{"--index": true}),
	"query":       configFlags.union(flagSet{"--index": true, "--depth": true}),
	"viz":         configFlags.union(flagSet{"--index": true, "--output": true}),
	"report":      configFlags.union(flagSet{"--index": true, "--output": true, "--force": false, "--docs": true}),
	"communities": configFlags.union(flagSet{"--index": true}),
	"export":      configFlags.union(flagSet{"--index": true, "--format": true, "--output": true}),
	"wiki":        configFlags.union(flagSet{"--index": true, "--output": true}),
	"hook":        flagSet{},
	"map":         configFlags.union(flagSet{"--index": true, "--top": true}),
	"risk":        configFlags.union(flagSet{"--index": true, "--top": true, "--by-file": false}),
	"toc":         configFlags.union(flagSet{"--index": true, "--json": false, "--output": true, "--config": true}),
	"build":       configFlags.union(flagSet{"--index": true, "--docs": true, "--include-submodules": false}),
}
