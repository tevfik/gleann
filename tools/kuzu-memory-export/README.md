# kuzu-memory-export

Exports the entity stores of gleann v1.9.x (`<IndexDir>/<name>_memory`, a Kùzu
database) to JSON so `gleann memory import-graph` can load them into
`memory.sqlite`. See "Upgrading entities from v1.9" in `docs/memory_engine.md`.

This is a separate Go module (`github.com/tevfik/gleann/tools/kuzu-memory-export`)
with the Kùzu binding (go-kuzu v0.11.3, CGo); the main gleann module does not depend on it.

```bash
make kuzu-memory-export          # builds bin/kuzu-memory-export in the repository root
kuzu-memory-export <store path | index dir> [-o dir]
```

* A path ending in `_memory` is one store (file or directory); any other folder is
  scanned for `*_memory` stores (also its `indexes` subfolder).
* Opens read-only, tolerates a missing `Entity_Archive` table. If the read-only open
  fails (for example a leftover `.wal`), it copies the store to a temp folder and opens
  the copy; the original files are never written.
* Output: `<dir>/<name>.kuzu-export.json`, format `gleann-kuzu-memory-export`, version 1:
  `{format, version, namespace, entities[{id,type,content,attributes}],
  relations[{from,to,relation_type,weight,attributes}],
  archive[{archive_id,id,version_id,type,content,attributes,valid_to}]}`.
  `attributes` are the stored JSON text; edge `created_at`/`updated_at` are inside it.

Tests (`go test ./...`, CGo) build stores with the v1.9 DDL (current schema,
pre-archive schema, uncheckpointed wal) and, when available, the real v1.9.0
fixture (`GLEANN_V190_MEMORY_FIXTURE`), whose export must equal
`pkg/memory/testdata/v190/mem.kuzu-export.json`.
