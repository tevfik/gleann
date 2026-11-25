package main

// The flag tables of the commands that scan their arguments by hand with
// hasFlag/getFlag. Each table lists exactly the flags the command (and the
// helpers it calls, such as getConfig) reads, plus flags that are accepted
// but ignored and are documented as such. A flag the table lacks is rejected
// by rejectUnknownFlags with exit status 2.

// modeFlags are read by parseIndexMode.
var modeFlags = flagSet{
	"--mode": true, "--code-only": false, "--code": false,
	"--docs-only": false, "--no-plugins": false,
}

// buildFlags is every flag of `gleann index build` and `index rebuild`.
// GRAPH_REPORT.md and AGENTS.md are only written on --report/--graph-report
// and --agents/--write-agents; the build generates neither automatically.
// "--no-report" and "--no-agents" are no longer in the help because the
// default already skips both, but they stay accepted so old scripts that
// pass them keep working.
var buildFlags = configFlags.union(modeFlags, flagSet{
	"--docs": true, "--graph": false, "--dry-run": false,
	"--max-minutes": true, "--approve": true, "--yes": false, "--json": false,
	"--progress": true, "--tags": true, "--tag": true, "--mcp": true,
	"--include-submodules": false, "--instant": false, "--lexical-only": false,
	"--report": false, "--graph-report": false, "--desc": true,
	"--multimodal-model": true, "--agents": false, "--write-agents": false,
	"--no-report": false, "--no-agents": false,
})

// indexFlagSets maps each `gleann index` subcommand to its flags.
var indexFlagSets = map[string]flagSet{
	"build":   buildFlags,
	"rebuild": buildFlags,
	"sync": configFlags.union(modeFlags, flagSet{
		"--docs": true, "--graph": false, "--instant": false, "--lexical-only": false,
		"--files": true, "--include-submodules": false, "--progress": true,
		"--multimodal-model": true,
	}),
	"watch": configFlags.union(modeFlags, flagSet{
		"--docs": true, "--graph": false, "--interval": true,
		"--include-submodules": false,
	}),
	"list": configFlags.union(flagSet{
		"--tag": true, "--tags": true, "--mcp": false, "--public": false,
		"--private": false, "--json": false,
	}),
	"info":   configFlags.union(flagSet{"--json": false}),
	"remove": configFlags,
	"tag": configFlags.union(flagSet{
		"--add": true, "--remove": true, "--set": true, "--clear": false,
	}),
	"set": configFlags.union(flagSet{
		"--public": false, "--private": false, "--mcp": true, "--desc": true,
	}),
	"storage": configFlags.union(flagSet{"--set": true, "--progress": true}),
	"repair": configFlags.union(flagSet{
		"--all": false, "--check": false, "--force": false, "--progress": true,
	}),
	// --rollback and --language were removed in v1.20; they stay accepted so
	// cmdMigrate can say so instead of a bare "unknown flag".
	"migrate": configFlags.union(flagSet{
		"--all": false, "--graph": false, "--cleanup": false,
		"--rollback": false, "--language": true,
	}),
}

// memoryFlagSets maps each `gleann memory` subcommand to its flags.
var memoryFlagSets = map[string]flagSet{
	"remember":     {"--tag": true, "--label": true, "--scope": true},
	"add":          {"--tag": true, "--label": true},
	"forget":       {"--all": false},
	"list":         {"--tier": true, "--json": false, "--scope": true},
	"search":       {"--json": false},
	"clear":        {"--tier": true},
	"stats":        {},
	"summarize":    {"--last": false, "--id": true, "--extract": false},
	"prune":        {"--age": true},
	"compact":      {"--min-validity": true},
	"context":      {"--json": false, "--verbose": false, "--path": true, "--command": true, "--symbol": true, "--scope": true, "--max-bytes": true},
	"maintain":     {},
	"export":       {"--dir": true, "--scope": true, "--include-short": false, "--include-expired": false},
	"import":       {"--dir": true, "--dry-run": false},
	"import-graph": {},
	"reflect":      {"--dry-run": false, "--revert": true, "--list": false, "--json": false, "--limit": true},
	"failure":      {"--command": true, "--error": true, "--fix": true, "--path": true, "--symbol": true, "--scope": true, "--json": false},
}

// serviceFlagSets maps each `gleann service` subcommand to its flags.
var serviceFlagSets = map[string]flagSet{
	"install":   {"--addr": true, "--port": true, "--bin": true},
	"start":     {"--addr": true, "--port": true, "--bin": true},
	"restart":   {"--addr": true, "--port": true, "--bin": true},
	"uninstall": {},
	"stop":      {},
	"status":    {},
	"logs":      {"--lines": true},
}

// tokenFlagSets maps each `gleann token` subcommand to its flags.
var tokenFlagSets = map[string]flagSet{
	"create": {"--scope": true},
	"list":   {},
	"revoke": {},
	"rotate": {},
	"show":   {},
}

// pluginFlagSets maps each `gleann plugin` subcommand to its flags. The
// "models" subcommand is not listed: its arguments belong to the plugin binary.
var pluginFlagSets = map[string]flagSet{
	"list": {}, "ls": {}, "install": {}, "add": {}, "info": {},
	"update":  {"--check": false, "--force": false},
	"upgrade": {"--check": false, "--force": false},
}

// configSubFlagSets maps each `gleann config` subcommand to its flags.
var configSubFlagSets = map[string]flagSet{
	"show": {}, "path": {}, "edit": {}, "validate": {}, "set": {},
}

// mcpFlags are the flags of `gleann mcp` (parsed with the flag package).
var mcpFlags = flagSet{
	"--clean-names": false, "--tools": true, "--llm-model": true, "--llm-provider": true,
}

// mcpInstallFlags are the flags of `gleann mcp install`.
var mcpInstallFlags = flagSet{"--target": true, "--bin": true, "--name": true}

var defFlags = configFlags.union(flagSet{"--index": true, "--kind": true, "--json": false})

var chatFlags = configFlags.union(flagSet{
	"--list": false, "--pick": false, "--show-last": false, "--show": true,
	"--delete": true, "--delete-older-than": true, "--classic": false,
	"--modular": false, "--repeat-penalty": true, "--repetition-penalty": true,
	"--api-key": true, "--session": true,
})

var serveFlags = configFlags.union(flagSet{"--addr": true, "--port": true})

var tasksFlags = flagSet{"--json": false, "--status": true, "--addr": true}

var benchFlags = configFlags.union(flagSet{
	"--index": true, "--docs": true, "--tasks": true, "--suite": true,
	"--output": true, "--json": false, "--gen-tasks": true, "--kind": true,
	"--count": true, "--agent": false, "--compare": true, "--per-task": true,
})

var agentsFlags = configFlags.union(flagSet{
	"--stdout": false, "--force": false, "--index": true, "--docs": true, "--dir": true,
})

var uninstallFlags = flagSet{"--all": false, "--purge": false, "--data": false, "--yes": false}

var installFlags = flagSet{
	"--user": false, "--system": false, "--dir": true, "--index": true,
	"--follow-symlink": false, "--list": false, "--platform": true, "--project": false,
}

var setupFlags = configFlags.union(flagSet{
	"--auto": false, "--quick": false, "--check": false, "--bootstrap": false,
	"--yes": false, "--docs": true, "--name": true, "--graph": false,
	"--force": false,
})

var updateFlags = flagSet{"--check": false, "--force": false}
