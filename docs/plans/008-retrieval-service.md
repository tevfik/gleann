# RFC 008: One retrieval service

Status: accepted by the maintainer on 2026-11-21 (audited 2026-11-20); not implemented yet
Builds on: ROADMAP W6 and item 5.1; WORKPLAN WP-5.1; `docs/architecture.md` "Planned refactors"; `internal/indexing` as the write-side precedent
Release: not assigned. No index format, `config.json` key, REST route, MCP tool name or CLI flag is renamed or removed.
Tree: main `11f4538` (v1.20.1) plus the uncommitted next-release work. Line numbers were written on 2026-11-20 and re-read on 2026-11-21 wherever the audit touched them; the working tree is still changing (section 11). Paths are relative to the repository root.
Audit record: `gleann-review/progress/wave-2026-11-20/008-audit.md` (35 findings, two lenses).

## 1. Problem & Motivation

Search has one implementation in the library (`pkg/gleann`, `LeannSearcher.Search`) and is wired by hand at every place that offers it. Each wiring decides again how the config is resolved, which embedder and reranker exist, which options are read, what a zero value means and what happens when the index changes. A setting that works on one surface and not on another is the result, release after release.

### 1.1 What is repeated

| What | Count | Where |
|---|---:|---|
| `gleann.NewSearcher` call sites outside tests | 9 in surfaces, 2 in the library | `cmd/gleann` 5, `internal/server/searcher_cache.go:119`, `internal/mcp/server.go:481`, `internal/tui` 2; `multi_search.go:110`, `multi_searcher.go:81` |
| Ways to resolve a `gleann.Config` | 5 | `getConfig`+`applySavedConfig` (`cmd/gleann/config.go`); `cmdChat`'s hand copy (`cmd_query.go:558-585`); `runMCPServer` (`cmd_mcp.go:58-110`) into `mcp.NewServer` (`mcp/server.go:149-162`); `tui.loadConfig` (`tui.go:329-359`); REST runtime updates (`ApplyConfig`) |
| Embedder construction | 5 | `indexing.NewEmbedder` and four inline `embedding.NewComputer` (`server.go:204`, `mcp/server.go:164` and `:470`, `tui.go:311`); REST `ApplyConfig` repeats the first inline one (`server.go:1932`) |
| Searcher caches | 2 | REST `searcher_cache.go` (331 lines); MCP `mcp/server.go:62-65, 375-690`. Same borrow/release shape, written twice; 27 `acquireSearcher` call sites (8 REST, 19 MCP) |
| Multi-index search | 5 | `SearchMultiple`; `MultiSearcher`; proxy `retrieveProxyContextLimited`; MCP `@tag` loop (`mcp/server.go:964-1031`); A2A (`a2a_handler.go:82-103`) |
| Index-name resolution and access checks (`mcp_exposed`, tags, client target) | 5 rules | section 3.8 has the table; no two entry points apply the same rule |
| Handling of an index changed by another process | 5 | both caches; `MultiSearcher.refresh`; `LeannChat.searchFresh`; the retry in REST `handleSearch` (`server.go:853`) |
| Default index for an empty name (MCP) | 2 | `mcp/server.go:377-406` and `:2374-2400` |

Only the first config path reads the saved `top_k`, `rerank_*`, `expand*`, `backend`, `storage_mode` and `GLEANN_INDEX_DIR`. The cache differences are not cosmetic: REST loads through singleflight with a generation counter and retries three times; MCP loads without a lock and closes the loser (`mcp/server.go:500-508`), checks access and rebuilds the embedder inside `acquireSearcher`, and attaches a reranker to the shared searcher at load (`:484-493`).

### 1.2 Option x surface matrix

R = settable per request; C = from config only; E = needs an environment variable; – = cannot be set; ✗ = accepted and dropped. Built by reading the handlers, not by running them.

| Option | CLI search | CLI ask | REST search | REST `/api/search` | REST ask | Proxy | Proxy pass-through | A2A | MCP search | MCP search_ids | MCP search_multi | MCP ask | MCP batch_ask | TUI chat |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| top_k | R | R | R | R | R | R | R | – | R | R | R | – | R | R |
| hybrid alpha | R | R | R | R | – | R | R | – | – | – | – | – | – | – |
| min score | – | – | R | R | – | R | R | – | – | – | – | – | – | – |
| rerank on/off | R | R | R | ✗ | – | – | R | – | E | E | – | – | – | R |
| rerank model / provider | R | R | R | ✗ | – | – | R | – | E | E | – | – | – | R |
| rerank pool | R | ✗ | R | – | – | – | R | – | – | – | – | – | – | – |
| rerank skip identifiers | – | – | – | – | – | – | – | – | – | – | – | – | – | – |
| metadata filters + logic | – | – | R | R | – | – | – | – | R | R | – | R | – | – |
| graph context | R | R | R | – | – | – | – | – | R | – | – | ✗ | – | – |
| include tests | R | – | R | R | – | – | – | – | R | R | R | – | – | – |
| kind (code/docs) | R | – | R | R | – | – | – | – | R | R | R | – | – | – |
| one per file | R | – | – | – | – | – | – | – | – | – | – | – | – | – |
| expand (HyDE) | R | R | R | – | R | – | – | – | R | – | – | – | – | – |
| expand model | R | R | C | – | C | – | – | – | – | – | – | – | – | – |

Of 196 cells, 64 are R, 4 are ✗, 4 work only with an environment variable set when the searcher was loaded, 2 are config only and 122 are unreachable (recounted by the audit). Only the Go API reaches every option. The same option has different names (`metadata_filters` in REST, `filters` in MCP; `--graph` and `graph_context`).

The matrix has 14 option rows; `retrieval.Options` (section 3.2) has 17 fields, because rerank model, provider and URL are one row here and filters and logic are one row. The checked form of this matrix (section 3.4, appendix A) is per field, so 17 x 14 = 238 cells.

The matrix says which names exist. It does not say what a zero or false value means, where the default comes from or what an invalid value does. Those differ too (section 1.6).

### 1.3 Bugs of this class already fixed

29 `CHANGELOG.md` entries in six releases (v1.8.1 to v1.20.1). Line numbers are those of `CHANGELOG.md`. The audit did not re-read the 29 entries; it re-read `:59`, `:128`, `:180`, `:182` and `:204-206`.

| Kind | Entries | Examples |
|---|---:|---|
| An option, flag or setting has no effect on one surface or path | 10 | `:182` `ask --rerank` and `ask --graph` had no effect, multi-index ask and `search --rerank` never reranked (four cases in one entry); `:206` `rerank_enabled` did not apply to CLI; `:128` saved `top_k` did not apply to CLI; `:59` REST ignored `"hybrid_alpha": 0`; `:63`, `:147`, `:185`, `:202`, `:211`, `:224-225` |
| The same operation implemented several times and fixed in each | 6 | `:180` cross-index ranking "the proxy, `MultiSearcher` and `SearchMultiple`"; `:120` a running server, MCP server or chat did not see another process's sync; `:60`, `:122`, `:132`, `:184` |
| One setting resolved differently per surface | 6 | `:43-48` endpoint and key (CLI, TUI, REST, MCP, A2A, proxy); `:205` reranker provider; `:268`, `:275`, `:282`, `:393` |
| Flag values leaking into the query, or silently ignored | 4 | `:125`, `:127`, `:183`, `:270` |
| Config applied or filled differently | 3 | `:53`, `:54`, `:144` |

23 of the 29 are option, flag or config kinds; 6 are "implemented several times". The step order of section 6 follows from that: the option and config kinds are covered first, without touching a cache.

### 1.4 The same class in the tree now

Found by reading; none was executed. Step 0c turns each into a test before it counts as a fact. Holes 17 to 21 were found during the audit.

| # | Hole | Where |
|---|---|---|
| 1 | `gleann ask --rerank-pool N` is accepted and not read | `flagspec.go:49`, `cmd_query.go:836`; only reader `cmd_search.go:109` |
| 2 | REST search `ef_search` and `recompute_embeddings` are decoded and in the OpenAPI schema; no handler reads them | `server.go:734-735`, `openapi_schemas.go:14-15` |
| 3 | REST `/api/search`: `rerank: true` sets the flag only, no reranker instance exists on that path; `rerank_model` unread; no `expand`, no `graph_context` | `multi_search_handler.go:25, 220-222`; `multi_search.go:110`; `searcher.go:414-420` |
| 4 | REST ask: only `top_k` and `expand` reach retrieval; `rerank_enabled` has no reranker here | `server.go:1004-1010` |
| 5 | Default-mode proxy does not read `X-Gleann-Rerank` and `-Rerank-Pool`; pass-through ignores `rerank_enabled` | `proxy_handler.go:164-179`, `proxy_passthrough.go:147-155` |
| 6 | MCP never receives the saved `top_k`, `rerank_*`, `expand*`; `rerank: true` is a no-op unless `GLEANN_RERANK` was set when the searcher loaded. `CHANGELOG.md:206` says `rerank_enabled` "already" applied to MCP; `docs/env-vars.md:44` says `GLEANN_RERANK` reranks "even when the config has the reranker off". Neither matches the code | `cmd_mcp.go:58-110`, `mcp/server.go:149-162, 484-493, 953-955` |
| 7 | MCP `gleann_ask` declares `graph_context`; the handler reads filters only | `mcp/server.go:1196-1199, 1226-1230` |
| 8 | MCP `gleann_search_multi` bypasses the cache, the per-index embedder and the reranker | `mcp/multi_search.go:69` |
| 9 | MCP `@tag` search merges by each index's own `Score`, the behaviour `:180` fixed on three other paths | `mcp/server.go:1026` |
| 10 | `MultiSearcher.Search` merges with a hard-coded `{TopK: 10, HybridAlpha: 0.7}`; `gleann ask a,b --top-k 20` is cut to 10 | `multi_searcher.go:145`; CLI ask passes no per-call options |
| 11 | The lexical-only path (alpha 0, no vectors, no embedder, embedder error) does not apply Kind, MinScore, IncludeTests or GraphContext; two of its returns also skip OnePerFile and the reranker | `searcher.go:463-498` |
| 12 | TUI chat: saved `expand` not applied; `gleann chat --top-k N` is overridden by the saved value or 10 | `tui/chat.go:272-293, 2129` |
| 13 | `SearchMultiple` applies `mcp_exposed` and `GLEANN_TAGS` for every caller, so `gleann search a,b` drops a private index without a message | `multi_search.go:24, 73-80` |
| 14 | OpenAPI `SearchRequest` has `op` where the struct tag is `operator`, and lacks `rerank_pool`, `include_tests`, `kind` | `openapi_schemas.go:25`, `filter.go:31` |
| 15 | Default top-k differs by entry point (5 or 10) | section 1.6 |
| 16 | `LeannChat.searchFresh` calls `Refresh` on whatever `*LeannSearcher` it wraps; `Refresh` is documented as unsafe while another goroutine searches. Chats wrap shared cached searchers in REST, A2A and MCP (`batch_ask` with up to 5 goroutines). A code path, not an observed failure | `freshness.go:252-258, 293-304`; `server.go:991`, `mcp/batch_ask.go:155` |
| 17 | REST: after `POST /api/config` the per-index embedders keep the previous host and provider. The resolver captures the config by value with a per-model cache, and the handler keeps the previous resolver; the settings path builds a new one | `cmd/gleann/config.go:462-477`, `server.go:1903`, `settings_wiring.go:40, 58` |
| 18 | REST: the cached expander is keyed by provider, base URL and model without the API key, and `ApplyConfig` does not clear the map, so a rotated key keeps being used | `server.go:131-141, 1929-1943` |
| 19 | CLI `--hybrid <not a number>` sets alpha to 0 (lexical only) without a message: the value is scanned into a fresh variable and assigned whether or not the scan succeeded | `cmd/gleann/config.go:137-143` |
| 20 | The proxy checks `mcp_exposed` and not `GLEANN_TAGS`. No reference to the variable or to the two REST access helpers exists in `internal/server/proxy_*.go`; whether a caller applies the tags before these files was not traced | `proxy_handler.go:355-387, 559-567` |
| 21 | REST single-index search and ask do not check `mcp_exposed`, so a private index is searchable through `POST /api/indexes/{name}/search` (the web UI path); MCP and `/api/search` refuse the same index. Possibly intended; decision D7 | `server.go:1674-1695` |

No existing test covers the class. `internal/doccheck` checks routes, environment variables, MCP tool names and CLI commands against the docs; `cmd/gleann/command_flags_test.go` checks that flags are accepted or rejected. The sibling repository `gleann-rag-test/e2e/parity_test.go` has PAR-01, 02, 03 and 06 (same ranking, `top_k`, flag position, `--flag=value`) as black-box tests; its `TEST_MATRIX.md:100-101` lists PAR-04 (option matrix) and PAR-05 (multi-index parity) as not written. Nothing walks `SearchConfig` against the surfaces.

### 1.5 Sizes

Non-test lines: `pkg/gleann` 20,397 in 77 files; `internal/server` 11,530; `internal/mcp` 5,999; `internal/tui` 12,189; `cmd/gleann` 13,024. Files: `internal/mcp/server.go` 2509, `internal/server/server.go` 2408 (ROADMAP W6 quotes 2336 and 2333; both grew), `pkg/gleann/searcher.go` 1791, `cmd/gleann/cmd_query.go` 1069. Functions on the retrieval path (from the source maps of the first draft, not re-counted): `LeannSearcher.search` 319 lines, `cmdAsk` 307, `LeannSearcher.Load` 242, `cmdSearch` 240, `handleMultiSearch` 202, `getConfig` 199, MCP `handleSearch` 197, REST `handleAsk` 169, MCP `acquireSearcher` 145.

Test construction sites that a move has to respect (counted for the audit, 2026-11-21): in `internal/server` tests, 174 `&Server{` literals in 37 files, 137 of them setting `searchers:` in 27 files, 21 calls of the package's `NewServer` in 9 files, 16 calls of `acquireSearcher`, 11 lines indexing `.searchers[`; in `internal/mcp` tests, 168 calls of `NewServer` in 32 files and 26 lines indexing `.searchers[`; 34 `Setenv("GLEANN_TAGS")` sites in 11 test files. The REST cache has one dedicated test file with two test functions (`searcher_cache_size_test.go`); the rest of its coverage comes through handler tests.

### 1.6 Value rules and defaults that differ today

The same request bytes mean different things per entry point. Each line below is a rule the move either keeps per entry or changes as a named change; none may change silently. "Kept" means: the entry's cell carries today's rule on day one (section 3.4). Sources were re-read for the audit unless marked.

**What zero, false and empty mean**

| Input | Rule today | Source |
|---|---|---|
| REST search `top_k`, `min_score`, `rerank_pool` | only a value above 0 counts | `server.go:814, 820, 843` |
| REST search `rerank`, `expand` | `request OR config`: an explicit `false` cannot switch off a saved setting | `server.go:840, 848` |
| REST search `graph_context`, `include_tests` | only `true` counts | `server.go:829, 832` |
| REST `hybrid_alpha` | presence counts, 0 is lexical only | `server.go:732, 817` |
| REST `filter_logic`, `kind` | any non-empty string is passed to the library | `server.go:826, 835` |
| MCP `gleann_search` and `gleann_search_ids` `top_k` | any number counts: 0 or a negative value falls through to the library's 10, an absent argument gives the entry's 5 or 10 | `mcp/server.go:934-937`, `search_progressive.go:105-108`, `searcher.go:407-410` |
| MCP `gleann_search_multi`, `gleann_batch_ask` `top_k` | only a value above 0 counts | `multi_search.go:59`, `batch_ask.go:115` |
| MCP `include_tests` | presence counts, `false` is passed explicitly | `mcp/server.go:947`, `multi_search.go:62` |
| MCP `filter_logic` | only `and` or `or`, anything else becomes `and`; always sent with filters | `mcp/server.go:872-876, 940-943` |
| Proxy `X-Gleann-Top-K` | only above 0; otherwise the config's top-k if above 0 | `proxy_handler.go:165-171` |
| Proxy `X-Gleann-Min-Score` | any parseable number, including 0 and negatives | `proxy_handler.go:172-176` |
| Proxy pass-through `X-Gleann-Rerank` | one header for two fields: any value except empty, `false`, `0` turns reranking on, and a value other than `true` or `1` is the model name | `proxy_passthrough.go:147-152` |
| CLI | `--rerank`/`--no-rerank`, `--expand`/`--no-expand` (the only off switches on any surface); two alpha flags, `--hybrid` (into the config, shared by all commands) and `--hybrid-alpha` (per request, search only); `--no-hybrid` is alpha 1.0 plus `SetScorer(nil)` on the single-index searcher | `cmd_search.go:75-77, 102-108, 298-303`, `expand_flags.go:9-14`, `config.go:137`, `flagspec.go:35, 57` |

**What an invalid value does**

| Input | Today | Source |
|---|---|---|
| REST `hybrid_alpha` outside [0, 1] or NaN | 400 | `server.go:750-755, 799` |
| Proxy `X-Gleann-Hybrid-Alpha` outside [0, 1] or not a number | ignored | `proxy_handler.go:462-472` |
| CLI `--hybrid-alpha` not a number | ignored; a number outside [0, 1] is passed on | `cmd_search.go:104-107` |
| CLI `--hybrid` not a number | alpha 0 (hole 19) | `config.go:137-143` |
| CLI `--rerank-pool` not a number or below 1 | ignored | `cmd_search.go:109-113` |
| MCP argument of the wrong type | ignored (type assertion) | `mcp/server.go:935-958` |
| REST or MCP name that no handler knows | ignored (decoder without `DisallowUnknownFields`, map lookups); the CLI rejects an unknown flag with exit status 2 | `server.go:790`, `flagspec.go:79, 154` |

**Where the default comes from**

| Default | Today | Source |
|---|---|---|
| top-k | 5: MCP `gleann_search`, `gleann_batch_ask`, MCP and CLI def, the ReAct search tool. 10 fixed: MCP `gleann_search_ids`, MCP prompts. Saved value or 10: REST, CLI, proxy, TUI | `mcp/server.go:934`, `batch_ask.go:114`, `def_tools.go:66`, `cmd_def.go:49`, `react.go:131`, `search_progressive.go:105`, `prompt_tools.go:69, 120`, `tui/chat.go:272-293` |
| forced options | MCP prompts force graph context on | `prompt_tools.go:69, 120` |
| rerank | CLI: saved setting or flag, with an off switch. REST search: config or request, no off switch. `/api/search`, REST ask, default proxy, A2A: never. Pass-through: header only. MCP: only with `GLEANN_RERANK` at load and `rerank: true`. TUI: saved value, then the session toggle | `cmd_search.go:298-303`, `server.go:840`, `mcp/server.go:484-493, 953-955`, `tui/chat.go:276, 1876-1886` |
| expand | CLI: saved or flag, with an off switch. REST search and ask: config or request. MCP: request only. Elsewhere never | `expand_flags.go:9-14`, `server.go:848, 1008`, `mcp/server.go:956-958` |
| expander construction | CLI: from the chat flags and `--expand-model`. Servers: from the config | `expand_flags.go:18-27`, `server.go:118-143` |
| cross-index fusion base | `SearchMultiple`: the config's top-k and alpha. `MultiSearcher`: fixed 10 and 0.7 (hole 10). MCP `@tag`: no fusion, own `Score` (hole 9). A2A: no merge at all | `multi_search.go:144-165`, `multi_searcher.go:145`, `mcp/server.go:1026`, `a2a_handler.go:82-103` |
| a failed index in a multi-index query | fail the call (`SearchMultiple`, `MultiSearcher`); skip and report (proxy); skip silently (MCP `@tag`, A2A) | `multi_search.go:128-130`, `proxy_handler.go:568-576`, `mcp/server.go:1005-1015`, `a2a_handler.go:84-92` |
| retry on `ErrIndexChanged` | REST search, `MultiSearcher`, the chat | `server.go:853`, `freshness.go:300-306` |

## 2. Goals and non-goals

| # | Goal | Pass condition |
|---|---|---|
| G1 | One request type and one service; every surface only translates its input into the request | test T4 (section 4): outside `pkg/gleann`, `internal/retrieval` and the allow-list, no non-test file names `gleann.NewSearcher`, `SearchMultiple`, `LoadMultiSearcher`, `NewMultiSearcher`, `NewReranker`, a `gleann.With*` option constructor, the types `gleann.SearchOption`, `gleann.LeannSearcher` or `gleann.MultiSearcher`, the mutators `SetReranker`, `SetScorer`, `SetQueryExpander`, or assigns a field of `SearchConfig` outside the config resolver |
| G2 | One searcher cache with one invalidation rule | one implementation; the MCP cache and the per-call open/close in `SearchMultiple` are not used by any surface |
| G3 | One place that turns config into request defaults and builds the reranker and expander | one function (`plan`, section 3.5); every `gleann.Config` comes from one resolver in `cmd/gleann` |
| G4 | The bug class cannot come back unnoticed | tests T1-T5 of section 4. They stop at `SearchConfig`: they show an option reaches the library, not that the library honours it on every path (hole 11; decision D16) |
| G5 | Results unchanged | for the same request bytes (argv, JSON body, headers, tool arguments), the same config file and the same environment, the output of every entry point is identical before and after each step, except for the changes named in section 6.3; the gates in `internal/quality` and `bench/vectordb/gate` stay green on the steps that edit `pkg/gleann` |
| G6 | The public Go API keeps compiling | `apidiff` on `pkg/gleann` reports no incompatible change at any step, for the default, `eif` and `treesitter` tag sets |

G5 is stated on request bytes, not on "effective options": a decoder that reads `{"rerank": false}` differently from today's handler produces different effective options from the same bytes, and the first draft's wording would not have counted that as a change.

Non-goals:
- New options, new surfaces, new endpoints or tools.
- Any change to ranking: boosts, RRF, the rerank stage, fusion constants.
- Changing what zero, false or an invalid value means on any surface. Each entry keeps its rule of section 1.6; changing one is closing a hole and follows the rule below.
- Presentation options that do not change which passages are retrieved (`--explain`, `--cite`, MCP `focus` and `max_tokens`, proxy `X-Gleann-Max-Context-Chars` and cover). They stay in their surface and are outside the matrix test.
- New work on the frozen surfaces (`docs/architecture.md:289-300`: TUI, proxy, A2A, multimodal, plugins). They move onto the service so the duplicated wiring can be deleted; they gain nothing else (decision D20).
- Merging the graph handle pools (`graphDBPool`, MCP `graphPool`) or the memory pools.
- A package-level split of `pkg/gleann` (section 3.11).

The holes of section 1.4 are bugs or open decisions. A step may change output only for an input covered by a section 1.4 test; that test is updated in the same commit and the change gets a CHANGELOG line. Section 6.3 lists every change of this kind that the plan makes.

## 3. Architecture & Data Structures

### 3.1 Package and dependencies

A new package `internal/retrieval`, the read-side counterpart of `internal/indexing`. It imports `pkg/gleann` and `internal/embedding`; `internal/embedding` already imports `pkg/gleann` and `pkg/gleann` does not import `internal/embedding`, so there is no cycle (checked with `go list -deps` for the default tags by the draft and both auditors, for `-tags eif` by one auditor, and for the default tags again on 2026-11-21). The surfaces import it. `pkg/gleann` cannot hold the service: it cannot build an embedder without importing `internal/embedding`, which imports it.

```
cmd/gleann, internal/{server,mcp,tui}     decode input, render output
        |
internal/retrieval                        Request, cells, decoders, plan, cache, targets, multi-index
        |                                 internal/retrieval/retrievaltest: recording service, preloaded searchers
pkg/gleann, internal/embedding            LeannSearcher, rerankers, embedders (unchanged API)
```

### 3.2 Request, options, response

```go
type Options struct {            // nil or empty = not given by the caller
    TopK                  *int
    HybridAlpha           *float32          // 0 is a valid value (lexical only)
    MinScore              *float32
    Rerank                *bool
    RerankModel           *string
    RerankProvider        *string
    RerankURL             *string
    RerankPool            *int
    RerankSkipIdentifiers *bool
    Filters               []gleann.MetadataFilter
    FilterLogic           *string
    GraphContext          *bool
    IncludeTests          *bool
    Kind                  *string
    OnePerFile            *bool
    Expand                *bool
    ExpandModel           *string
}

type Request struct {
    Query        string
    Indexes      []string   // names, "@tag", "@all"; empty = the entry's default-target rule
    ClientTarget string     // REST only: the restriction from X-Gleann-Target/-Index or ?target=/?index=
    Options      Options
    Entry        Entry      // which entry point is calling (section 3.4); the zero value is rejected
    Path         string     // which handler path of the entry ("ask", "ask.stream"); used by T5
}

type Response struct {
    Hits     []Hit                              // fused across indexes per the entry's fusion rule, cut to top-k
    Parts    map[string][]gleann.SearchResult   // per index, before fusion (the proxy's cover selection needs it)
    Applied  gleann.SearchConfig                // the planned config handed to the library (section 3.5)
    Warnings []Warning                          // index + text, from LeannSearcher.Warnings
    Skipped  []Skipped                          // index + reason; always filled, rendered per surface
}
type Hit struct { gleann.SearchResult; Index string }   // never marshalled; surfaces render their own shape
```

The pointer fields give the type room for "not given". They do not decide when a decoded value counts as given: that is the cell's presence rule (section 3.4), which on day one is today's rule for that entry. `{"rerank": false}` on REST search therefore stays "not given" after the move.

`Hit` is an internal type. A surface renders `MultiSearchResult.Index` or `Metadata["_index"]` as it does today; a view (section 3.9) sets `Metadata["_index"]` itself. `Applied` is what `plan` computed; the library still replaces a top-k of 0 or less by 10 on its own (`searcher.go:407-410`), and `Applied` carries the planned value, not that substitution.

### 3.3 Service API, ownership, errors

```go
type Deps struct {
    Config       func() gleann.Config                          // required; REST and MCP swap it at run time
    Embedder     func(gleann.Config) gleann.EmbeddingComputer  // nil: embedding.FromConfig
    ChatConfig   func(gleann.Config) gleann.ChatConfig         // for the expander; nil: the rule of server.go:118-131
    AllowedTags  func() (tags []string, set bool)              // read per call; required when an entry's policy checks tags
    DefaultIndex func(indexDir string) string                  // the entry's default-target rule for an empty name; nil: none
    CacheBudget  func() int64                                  // bytes, read per eviction; nil: 0
    MaxSearchers int                                           // count cap, 0 = none
    Logf         func(format string, args ...any)              // nil: log.Printf
}
func New(Deps, ...Option) (*Service, error)                    // nil Config is an error

func (s *Service) Search(ctx context.Context, r Request) (Response, error)
func (s *Service) Plan(r Request) (Plan, error)                // steps 4-8 only, see section 6
func (s *Service) View(ctx context.Context, r Request) (*View, error)
func (s *Service) Acquire(ctx context.Context, e Entry, index string) (Handle, func(), error)
func (s *Service) Definitions(ctx context.Context, e Entry, index, query string, o gleann.DefinitionOptions) ([]gleann.DefinitionResult, error)
func (s *Service) ReadDocumentTool(ctx context.Context, e Entry, index string) (gleann.ReActTool, func(), error)
func (s *Service) Invalidate(index string)
func (s *Service) InvalidateAll()
func (s *Service) OnInvalidate(fn func(index string))
func (s *Service) Close() error

type Handle interface {          // what read, graph and cite code needs; it has no Search method
    GraphDB() gleann.GraphDB
    PassageManager() gleann.PassageStore
    ExplainResult(ctx context.Context, query string, result gleann.SearchResult, opts gleann.ExplainOptions) (*gleann.Explanation, error)
    Warnings() []string
    Meta() gleann.IndexMeta
    Embedder() gleann.EmbeddingComputer
    EmbeddingPrefixes() (passage, query string)
}
```

`Acquire` and `Definitions` take the entry. In the MCP server the default-target rule and the access check live inside `acquireSearcher` today and protect all 19 call sites, including read, graph, def and prompts (`mcp/server.go:377-432`). The service resolves an empty index with the entry's default-target rule and applies the entry's access policy before borrowing, so those checks keep one home.

`Acquire` returns `Handle`, not `*gleann.LeannSearcher`. The seven methods are the ones the surfaces call on a borrowed searcher today apart from `Search`, `Load`, `Close` and the freshness calls (counted with grep over the non-test files of `internal/mcp` and `internal/server`). With no `Search` on the handle, searching around the service does not compile. The signatures are those of `*LeannSearcher` (`searcher.go:885, 895, 901, 1734, 1784`, `explain.go:115`, `prefixes.go:46`), so it satisfies the interface without a change to `pkg/gleann`. Decision D3.

`ReadDocumentTool` exists because `gleann.NewReadDocumentTool` takes the concrete searcher (`react.go:175`) and agent mode adds the tool only for a single index (`cmd_query.go:452-454`). The service builds the tool on a borrowed searcher and returns the release function; `pkg/gleann` gets no new function.

`embedding.FromConfig` is the body of `indexing.NewEmbedder` (`internal/indexing/env.go:22-33`) moved down one package; `indexing.NewEmbedder` forwards to it. It replaces the inline `embedding.NewComputer` calls on the read path (MCP `server.go:164, 470`, `tui.go:311`). The REST server's inline embedder (`server.go:204-209`, `:1932-1937`) is also the embedder of its write handlers (`:1264-1271`, `:1391-1397`), built without `BatchSize` and `Concurrency`. This RFC leaves the write-side embedder as it is; only searcher loads get their embedder from the service. The "model mismatch" check on the read path (`searcher_cache.go:109-113`) is unreachable in production, because `cmd_serve.go:123` always sets a resolver; the same check in the write handlers stays.

#### 3.3.1 Ownership and lifetime

- **REST.** `server.NewServer(config, addr, version)` keeps its signature. The server owns the config (`cfgMu`, swapped by `ApplyConfig`, `server.go:1929-1943`), so it builds its own service after its config fields are set, with `Deps.Config` the method value `s.cfg`. A `Server` built as a struct literal, as 174 test sites do, gets its service on first use (a `sync.Once` in the server). `Stop` closes it.
- **MCP.** `mcp.NewServer(cfg)` keeps its signature and builds its service the same way; `Close` closes it. The MCP config is not constant either: a roots change rewrites `IndexDir` (`roots.go:43-47`), so `Deps.Config` is an accessor here too.
- **CLI.** Each command function builds one service after config resolution, passes it as a parameter to an inner function (`runSearch(svc, args)`, `runAsk(svc, args)`), and closes it on the normal return. The error paths call `exitFunc(1)` (`cmd_search.go:155-158`), which skips deferred calls; that is today's behaviour for a loaded searcher (`cmd_query.go:284` defers `Close`, `:457-460` exits) and is kept. Decision D10.
- **TUI.** `cmd/gleann` builds the service and passes it into the TUI entry functions; the package variables `tui.SearcherLoader` and `searchIndexDir` (`agentchat_scope.go:20, 49`) go.
- No package-level variable holds a `*Service` or an `Entry` (AGENTS.md 4.1). T4 checks the first syntactically.
- **Order on a config swap.** The owner stores the new config first, then calls `InvalidateAll`, as `ApplyConfig` does today. The service cannot see a change behind `Deps.Config`.
- **Tests.** Two options of `New`, used only from `_test.go` files and from `retrievaltest` (T4 checks that): `WithObserver(func(Observation))`, called once per planned search with the entry, path, index, decoded `Options` and planned `SearchConfig`; and `WithLoader(...)`, which replaces "`NewSearcher` + `Load`" so a test can supply a prepared searcher. `retrievaltest.NewRecording(t, deps)` and `retrievaltest.Preloaded(t, searchers)` wrap them. Tests no longer reach into `searchers` maps.

#### 3.3.2 Errors

The service raises four errors of its own, exported and returned wrapped (`fmt.Errorf("<action> %q: %w", ...)`):

| Sentinel | Raised when | REST today | MCP today | CLI today |
|---|---|---|---|---|
| `ErrBusy` | every load attempt was invalidated (`errSearcherBusy`, `searcher_cache.go:98`) | 503 with `Retry-After: 1` (`:324-328`); proxy skip reason "busy; retry" | no such state today; after step 13 the "index is being built" answer (`mcp/server.go:1942-1955`) | not reachable (one process, no concurrent writer in it) |
| `ErrAccessDenied` | the entry's access policy refuses the index; the message text is the entry's, as today | 403 (`server.go:784-787`, `multi_search_handler.go:126, 168`) | tool error "Error loading index %q: " followed by the text of `mcp/server.go:418, 429` (`:1967`) | not raised (no policy on single-index CLI entries) |
| `ErrInvalidName` | `gleann.ValidateName` fails; wraps its error | 400 "invalid index name: ..." (`server.go:779-781`) | tool error "Error loading index %q: ..." (`mcp/server.go:410-412, 1967`) | not re-read |
| `ErrNoTarget` | empty index and the default-target rule finds none | not raised | the text "index name required (use gleann_list to see available indexes)" (`mcp/server.go:408`) inside the same tool error | usage text |

Errors from the library (`Load`, `Search`) are returned as they are, without an added prefix. The surfaces print them inside their own text ("index %q not found: %v", "error loading index: %v", "search failed: ..."), and a second prefix would change every rendered message and with it the error goldens. `gleann.ErrOldStoreFormat` (REST 409, `searcher_cache.go:320-323`) and `gleann.ErrIndexChanged` keep working with `errors.Is`. Everything else from a load is rendered by REST as 404, as today (`:330`).

The MCP server decides "index is being built" by substring on the error text (`timeout`, `lock`, `bbolt`, `resource temporarily unavailable`, `status 1`; `mcp/server.go:1946-1951`). The test runs on every error of `acquireSearcher`, including the access errors, whose text contains the index name (`:418, 429`): by reading, a private index whose name contains `lock` or `timeout` is answered with "is currently being built or synced", not with the access message. Step 13 puts `errors.Is` checks for the service's own sentinels in front of the substring test, so access, name and target errors no longer reach it, and adds `ErrBusy` to the busy branch (a named change for such index names, section 6.3). The substring test itself stays for library errors, because the library has no sentinel for a lock timeout and adding one is outside this RFC. Appendix C lists what was read of each surface's error rendering.

### 3.4 Cells, entries, decoders

The checked matrix is keyed by (entry, `Options` field), not by (input family, field). The first draft's family-level table could not hold the code: names and value rules differ between two entries of one family (`--hybrid-alpha` exists on search only; MCP `top_k` counts 0 on one tool and not on another), one field has several inputs (`--rerank`, `--no-rerank`), and one input sets two fields (`X-Gleann-Rerank`).

```go
type Cell struct {            // one per (entry, Options field)
    Inputs   []Input          // empty: the caller cannot set the field here
    Default  Default          // FromConfig | Const(v) | Off
    Requires []Field          // RerankModel requires Rerank; RerankPool requires Rerank; ExpandModel requires Expand
    Exempt   string           // reason; required when Inputs is empty
    Dropped  []string         // names still accepted and ignored (the ✗ cells)
}
type Input struct {
    Name     string           // "--rerank", "rerank", "X-Gleann-Rerank", "top_k"
    Presence Presence         // Present (any decoded value counts) | NonZero (zero, false, empty = not given)
    Negated  bool             // --no-rerank
    Const    any              // --no-hybrid: HybridAlpha 1.0
    Also     Field            // X-Gleann-Rerank: a value that is not a boolean is also RerankModel
    Invalid  InvalidPolicy    // Reject (the surface's status or exit code) | Ignore | Pass (handed to the library)
    Values   Sentinels        // one valid, one zero, one invalid value for T2 and T5; valid enum members for kind and filter_logic
}
```

`request OR config`, the rule of REST `rerank` and `expand`, is `Presence: NonZero` with `Default: FromConfig`. A forced option (MCP prompts' graph context) is a cell with no input and `Default: Const(true)`.

**Rule.** A decoder reproduces the entry's present treatment of absent, zero, false, negative and malformed values. Changing one is closing a hole and follows the rule of section 2. Appendix A holds the cells that were read for this revision, with their source lines; step 1 fills the rest from the handlers, and the complete 17 x 14 table is the initial content of `entries.go`.

**Entries.** An entry is an unexported value behind an exported function (`func MCPSearch() Entry`, `func Entries() []Entry`); the function returns a copy, including a copy of its cell data. The first draft's claim that "a value can only come from `entries.go`" does not hold in Go: `retrieval.Entry{}` compiles in any package, and exported `var`s can be reassigned by any importer. What holds is this:

- `Search`, `Plan`, `View`, `Acquire`, `Definitions` and `ReadDocumentTool` return an error for an entry whose name is empty. A request without a registered entry is rejected at run time, not at compile time.
- The zero access policy is the most restrictive one (`mcp_exposed` and tags), never "no check".
- An entry whose policy checks tags fails closed when `Deps.AllowedTags` is nil.

An entry carries: its family, its cells, its handler paths (`paths: {"ask", "ask.stream"}`), its access policy and default-target rule (section 3.8), its fusion rule and multi-index failure policy (section 3.8), and its expander source.

Kinds of entry:

| Kind | What | Count |
|---|---|---|
| search entries | the 14 columns of section 1.2. Families: CLI (argv), JSON (REST body), Header (proxy), MCP (argument map), Session (TUI: named setters on a session builder, no input format), None (A2A: every cell has no input and a default) | 14 |
| handle entries | callers of `Acquire`, `Definitions`, `ReadDocumentTool`: MCP read, graph, def and prompt tools, the REST and A2A handle sites. They have an access policy, a default-target rule and paths, and no option cells | set in step 1 from the 27 `acquireSearcher` sites |
| fixed callers | take no user input and pass literal options: MCP prompts (`prompt_tools.go:69, 120`), `recallVector`, the benchmark strategies. A separate type built by named constructors, with no cells and no exempt lines | set in step 1 |

**Decoders.** Four decoders share code per family and read the entry's cells: `FromArgs`, `FromJSON`, `FromHeader`, `FromMCP` (a `map[string]any`, so no MCP library import). Each returns `Options` and, for `Reject` inputs, an error that carries the surface's status. The Session family builds `Options` through setters named in the entry's cells; A2A builds an empty `Options`.

What "cannot be set" means differs per family and T5 checks exactly this: on the CLI an unbound flag is rejected with exit status 2; on REST and MCP an unbound name is ignored, which at the wire is the same as "accepted and dropped". The `Dropped` list exists so that the four ✗ cells and the unread REST fields are written down, not because the wire can tell them apart.

`--top-k` and `--hybrid` are in `configFlags`, shared with build and every other command (`flagspec.go:30-43`). For `search` and `ask` their values reach retrieval through the decoder (cells `TopK` and `HybridAlpha` of `cli.search` and `cli.ask`); the config resolver still accepts the flags but no longer writes them into `SearchConfig` for these two commands. A request value then has one path, and T5 cannot pass through the config path when a decoder is broken.

**Derived from the cells** instead of written by hand: the option part of `searchFlags` and `askFlags`, the option properties of the OpenAPI `SearchRequest` and of the MCP tool schemas, and the option lines of `--help`. A name that is declared is therefore a name that is decoded (holes 1, 2, 7, 14). Today's description texts differ per tool (`mcp/server.go:812-850` says "default 5" and quotes benchmark numbers for `expand`), and tool descriptions are what agents read. Each input therefore has its own description string, initialised to today's text; the derived output is identical on day one and step 0b pins it (`tools/list`, the OpenAPI document, `search --help`, `ask --help`). Decision D14.

### 3.5 Defaults and instances: `plan`

One function turns a `Request` into what the library call needs.

1. **Value per field**: the request value if the cell's presence rule counts it as given; otherwise the cell's default: `Const` (the entry's top-k of 5, a forced option), `Off`, or `FromConfig`, which is the field of `Deps.Config().SearchConfig` as it is. There is no fourth level. `SearchConfig` is a value type in which 0 is a valid alpha and `false` a valid `RerankSkipIdentifiers` (`types.go:590, 613-614`), so "unset in the config" cannot be observed; filling defaults is the config resolver's job, which starts from `gleann.DefaultConfig()`. A top-k of 0 or less is left to the library.
2. **Reranker**: when the effective `Rerank` is true, `gleann.NewReranker(gleann.ResolveRerankerConfig(cfg, model))` with the request's provider and URL applied. No surface calls `SetReranker` on a searcher, so a cached searcher carries no per-request state.
3. **Expander**: when the effective `Expand` is true, an expander built from `Deps.ChatConfig` with the effective expand model, as `Server.queryExpander` does today. `cmd/gleann` passes its own `ChatConfig` function (today `buildChatConfig(args, config)`, `expand_flags.go:18-27`); whether the CLI and server rules differ for any input was not checked, and the step 0b golden for `--expand` records the request the stub LLM received.
4. **Result**: a complete `gleann.SearchConfig` (`Plan.Config`) and one `gleann.SearchOption` that assigns it over the searcher's own copy, keeping only the load-time field `MaxBM25Passages`. `SearchOption` is `func(*SearchConfig)` (`searcher.go:910`), so this needs nothing new from the library. Because every field is set by the service, the `SearchConfig` a cached searcher was constructed with (`searcher.go:402`) cannot supply a default the service did not choose.
5. `plan` rejects a config whose `SearchConfig.CustomReranker` or `.Expander` is set. `cmdAsk` passes both through the config today (`cmd_query.go:795-805`); after step 4 they exist only in `plan`'s result.

**Instance maps.** The service keeps rerankers, expanders and per-model embedders. All three are fields of `*Service`, guarded by its mutex, cleared by `InvalidateAll` and `Close`.

| Map | Key | Bound | Note |
|---|---|---|---|
| rerankers | resolved `RerankerConfig` including endpoint and a hash of the key | 8, least recently used | `rerank_model` and the `X-Gleann-Rerank` value are strings a client controls; today REST builds a reranker per request and drops it (`server.go:841-842`) |
| expanders | provider, base URL, model, hash of the key | 8, least recently used | each `LLMQueryExpander` keeps its own query cache with no limit (`expand.go:33-34`); that is library code and not changed here |
| embedders | provider, endpoint, hash of the key, model, batch size, concurrency | none (keys come from the config and the index metadata) | replaces `defaultEmbedderResolver` (`cmd/gleann/config.go:462-477`) and MCP's inline rebuild (`mcp/server.go:465-479`) |

Including the credential in the key and clearing on `InvalidateAll` closes holes 17 and 18 when REST moves onto these maps (named changes, section 6.3). `gleann.NewReranker` reads `OLLAMA_HOST` and provider key variables when a config leaves them empty (`reranker.go:127-158`), so "the service never reads the environment" holds for the service's own code, not for the library below it. Whether a reranker or embedder instance holds model memory under `-tags eif`, and whether an evicted one must be closed, was not checked (section 5; measured in step 0e).

### 3.6 Config resolution

Config resolution stays in `cmd/gleann`, the only package that imports both the saved-config type (`tui.OnboardResult`) and the servers. `getConfig` + `applySavedConfig` become one `resolveConfig(args, profile)`; `cmdChat` and the TUI use it in step 3 and `runMCPServer` in step 13.

The profile exists because the four paths differ in more than the fields of holes 6 and 12. Appendix B lists the fields read for the audit: the index directory itself (the MCP server does not read `GLEANN_INDEX_DIR`, `gleann chat` does not apply the saved `index_dir`), endpoint normalisation and the `OLLAMA_HOST` fallback, `GLEANN_MMAP`, and the saved rerank and expand settings. A profile says, per field, which sources apply for that command. On day one each profile reproduces today's values; every difference removed later is a named change with a CHANGELOG line (decisions D5, D6).

Environment values that are policy, not config, do not go into `gleann.Config`:

- **`GLEANN_TAGS`** is an operator-set isolation boundary (`server.go:607-610`). `gleann.Config` has no tags field, and adding one would make the boundary writable through `POST /api/config`, which decodes a full config from the client (`server.go:1883-1914`). The value reaches the service through `Deps.AllowedTags`, read per call, as today; this keeps the 34 test sites that set the variable, several of them after the server value exists (`multi_search_handler_test.go`, `server_test.go`). The REST and MCP servers pass `retrieval.TagsFromEnv`, the one parser: elements are trimmed and empty ones skipped, and a variable that is set but has no non-empty element (for example `,`) refuses every index. That last case is today's rule in the MCP server, `/api/search` and A2A (`mcp/server.go:420-430`, `server.go:1620-1633`); `checkIndexAccess` and `SearchMultiple` treat it as unset (`server.go:1689`, `multi_search.go:64`). `SearchMultiple` keeps its own read for outside callers (section 3.10). Decision D11.
- **`GLEANN_INDEX`** and the working-directory rule reach the service through `Deps.DefaultIndex`, supplied by the MCP server; the two copies of the rule become one.
- **`GLEANN_SEARCHER_CACHE_BUDGET`** overrides the config at every eviction today (`searcher_cache.go:198-207`). `Deps.CacheBudget` is a function for that reason; copying the value into the config once would lose the precedence after a run-time config change.
- **`GLEANN_RERANK`, `_MODEL`, `_PROVIDER`** are read by the MCP server when a searcher loads (`mcp/server.go:484-493`). Their meaning after the move is decision D4; until step 13 nothing about them changes except the one named change of step 7 (section 6.3).

### 3.7 Searcher cache

A new type in `internal/retrieval/cache.go`, with the REST cache's behaviour; the MCP cache is deleted in step 13.

- Key: index name. Load: singleflight per name, on a context detached from the request, 10 minute limit.
- Borrowing: reference counted; a borrow returns a release function.
- Freshness on every acquire: `Change()`; a delta-only change is absorbed with `RefreshDelta`; anything else retires the searcher and loads a new one; three attempts, then `ErrBusy`.
- `ErrIndexChanged` from a search: the service releases, acquires again and searches once more, on every entry point. Today only REST search, `MultiSearcher` and the chat do; elsewhere this turns an error into a result (named change, section 6.3).
- Eviction: least recently used past `Deps.CacheBudget()` and past `MaxSearchers`. The budget compares `EstimatedSize`, which sums the sizes of the index files on disk (`searcher.go:68-91`: `.index`, `.delta`, `.db`, `.meta.json`, WAL and SHM), not resident memory; retired searchers that are still borrowed are not counted (`searcher_cache.go:236-241`). Defaults: decision D9.
- Embedder per index: through `Config.EmbedderResolver`, which the service always sets from its embedder map.

**Generations.** The REST cache has one generation number for the whole cache: `invalidateSearcher(name)` increments it (`searcher_cache.go:265-273`) and any load in flight, of any index, is then discarded (`:103-105, 124-131`). With a watch that syncs index A every few seconds, loads of unrelated indexes that take longer than the interval are discarded three times and answered 503. `/api/search` and `gleann_search_multi` are not exposed to this today because they open their own searchers; through one cache they would be. The new cache keeps a generation per index name plus one cache-wide generation that only `InvalidateAll` increments; a load is discarded when either moved.

**Invalidation rule, the only one.** A cached searcher is retired when
(a) an in-process writer calls `Invalidate(name)`: before a write that replaces or removes index files (delete, storage switch, MCP sync, rebuild; today `server.go:1477`, `storage_handler.go:81`, `mcp/server.go:2054`, which is what lets delete and storage switch work on Windows), and again after every write (`server.go:1482, 2231-2233`, `storage_handler.go:83`, `mcp/server.go:2186`);
(b) the config it was built from changes or the service closes (`InvalidateAll`: REST `ApplyConfig`, MCP roots change, shutdown);
(c) the freshness check on acquire finds a change that is not delta-only;
(d) the budget or the count cap evicts it.
Retired means removed from the map at once and closed when its last borrower releases it. `OnInvalidate` lets the two graph handle pools drop their handle on the same event; by reading, an MCP roots change clears searchers today and leaves `graphPool` on the old directory (`roots.go:43-65`).

An in-process incremental sync (watch, `gleann_sync`) calls `Invalidate` and so reloads the whole searcher, while the same sync done by another process is absorbed in place by rule (c). This RFC keeps that, because it is today's behaviour and no measurement of the reload cost exists. Decision D17.

The CLI uses the same service with a cache of one process lifetime and no cap. The TUI calls `Invalidate` for the indexes that leave the scope on a scope switch, which keeps today's memory behaviour (`agentchat_scope.go:277-285` closes the previous searcher).

### 3.8 Targets, access, multi-index

One implementation, `targets.go` and `multi.go`.

1. **Expand targets**: names, `@tag`, `@all`, the `<name>-code`/`<name>-docs` pair (today duplicated in `cmd_search.go:125-142` and `cmd_query.go:772-789`), and the entry's default-target rule for an empty list.
2. **Apply the entry's access policy.** The policy always reads the index metadata from disk, never from a cached searcher, so a `mcp_exposed` change made by the CLI applies to a running server at once.
3. **Search** each index through the cache, in parallel with a limit, collecting parts in name order.
4. **Fuse** per the entry's fusion rule, cut to the effective top-k.
5. **On a failed index** follow the entry's policy and fill `Response.Skipped`.

One index is the same path with one part and no fusion. The proxy keeps `rankProxyHits` and `selectProxyHits` and feeds them from `Response.Parts`.

**Access per entry, as read.** Every row is kept as it is by the move. Aligning two rows is a step 18 item with its own test and CHANGELOG line (decisions D7, D8, D19).

| Entry | `mcp_exposed` | `GLEANN_TAGS` | client target | metadata unreadable | a refused index |
|---|---|---|---|---|---|
| CLI search, one index | no | no | – | load fails | – |
| CLI search, several (`a,b`, `--all`, code/docs pair) | yes | yes | – | kept (the check runs only when the metadata was read) | dropped without a message (hole 13) |
| CLI ask, one or several | no | no | – | load fails | – |
| REST search, REST ask | no (hole 21) | yes | yes | open | 403 with the text of `server.go:1607, 1690` |
| REST `/api/search`, a named index | yes | yes | yes (body, header, query) | 404 | 403 "access denied: index %q is private or does not match required tags" (`multi_search_handler.go:162-170`) |
| REST `/api/search`, `@tag` or no target | yes | yes | yes | – | left out; an empty result is 200 with no message (`:152-156, 174-200`) |
| Proxy, both modes | yes | no (hole 20) | – | open | alone: error "index %q is private and not exposed via proxy"; among several: left out and logged, not listed in the skipped header (`proxy_handler.go:559-567`) |
| A2A search | yes | yes | – | open when the index directory exists (`server.go:1642-1654`) | named: error "access denied or index %q not accessible"; listed: left out (`a2a_handler.go:60-79`) |
| MCP, every tool that takes an index | yes | yes (elements not trimmed, empty ones not skipped, `mcp/server.go:420-427`) | – | open | tool error "Error loading index %q: access denied: ..." (`mcp/server.go:418, 429, 1967`); `gleann_batch_ask` says "index %q not found: ..." (`batch_ask.go:128-131`) |
| MCP `gleann_search` with `@tag` | yes | yes | – | – | left out without a message (`:980-999`) |

The second filter inside `SearchMultiple` (`multi_search.go:60-88`) repeats the REST handler's check with the same rule and changes nothing on that path; for the CLI it is the only check.

**Default target per entry.** MCP: `GLEANN_INDEX`, then an exposed index named like the working directory, then the single exposed index (`mcp/server.go:377-406`); `gleann_search` additionally falls back to `@all` when more than one index is exposed (`:917-931`), and `gleann_batch_ask` requires a name before it borrows (`batch_ask.go:108-111`). The rule is therefore per path, not per server. REST `/api/search`: all accessible indexes. A2A: all accessible indexes in list order. CLI, REST single-index routes: a name is required.

**Fusion and failure policy per entry, as today.**

| Entry | Fusion | Failed index |
|---|---|---|
| CLI search (several), REST `/api/search`, MCP `gleann_search_multi` | `CrossIndexScores`, base top-k and alpha from the config | fail the call |
| CLI ask (several), TUI (several) | `CrossIndexScores`, base fixed at top-k 10 and alpha 0.7 (hole 10) | fail the call |
| MCP `gleann_search` with `@tag` | none: sort by each index's own `Score` (hole 9) | skip; not rendered |
| Proxy | parts only; the proxy ranks them itself | skip and report (`X-Gleann-Skipped-Indexes`) |
| A2A | see below | skip; not rendered |

`Response.Skipped` is always filled. Whether a surface renders it is the surface's choice; MCP `@tag` and A2A do not render it on day one, so their output does not change.

**A2A** is not a fused multi-index search. It searches the accessible indexes one after another in list order with no options, appends every hit of an index, and stops once at least 10 lines exist, so the count is not capped at 10 and later indexes are never searched (`a2a_handler.go:82-103`). It cannot go through a fused `Service.Search` and stay identical. The handler keeps its loop and calls `Service.Search` once per index with one target, which is a search without fusion. This is the same pattern the proxy uses with `Parts`, and it is consistent with A2A being a frozen surface. Decision D8.

### 3.9 ask, handles, def

**ask** is retrieval plus generation. `LeannChat` depends only on `gleann.Searcher`. Surfaces give it a view:

```go
func (s *Service) View(ctx context.Context, r Request) (*View, error)
func (v *View) Search(ctx context.Context, query string, opts ...gleann.SearchOption) ([]gleann.SearchResult, error)
func (v *View) Close() error       // releases nothing shared; never closes the service or a cached searcher
func (v *View) Targets() []string  // resolved index names
```

- `View` resolves the targets, applies the access policy and acquires each index once before it returns, so a missing index fails where it fails today: `createAskSearcher` exits with "error loading index" before any LLM call (`cmd_query.go:808-820`), and the TUI reports "load a, b: ..." at the scope switch (`agentchat_scope.go:272-275`). It holds no borrow between searches.
- `View.Search` runs `Service.Search` with the view's request. Options passed per call are applied on top of the planned config; after step 17 no surface passes any, and what `pkg/gleann` itself passes inside `LeannChat` was not checked.
- Over two or more targets the results carry `Metadata["_index"]` on a copied map, as `MultiSearcher` sets it (`multi_searcher.go:133-139`); the library and the TUI read that key (`token_budget.go:229, 256`, `tui/agentchat.go:364`, `agentchat_scope.go:162`). Over one target the key is absent, as with `LeannSearcher`.
- A view is not a `*LeannSearcher`, so `searchFresh` no longer calls `Refresh` on a shared searcher (hole 16); freshness is the cache's.

A view is also not a `*MultiSearcher`, and five places test for the concrete types today. Each stops working without an error unless it is replaced:

| Site | Today | With a view |
|---|---|---|
| `LeannChat.SetReranker` (`chat.go:689-698`), called by the TUI's rerank toggle (`tui/chat.go:1876-1886`) and after a scope switch (`agentchat_scope.go:280`) | type switch with no default: a no-op on any other searcher | the TUI session rebuilds the view with `Options.Rerank` (and the selected model) and calls `chat.SetSearcher`. `LeannChat.SetReranker` is not changed |
| `graphSearcher` for @-mentions (`tui/agentchat.go:731-746`) | the searcher, or the first sub-searcher with a graph | `Acquire` on each of `view.Targets()` until a handle has a graph |
| `/graph` (`tui/chat.go:2344-2348`) | "requires a single index" unless `*LeannSearcher` | one target: `Acquire`; several: the same message |
| `printAttribution` for `ask --cite` (`cmd_query.go:1049-1058`) | embedder and prefixes from `*LeannSearcher`, otherwise `newEmbedder(config)` with empty prefixes | one target: `Handle.Embedder()` and `EmbeddingPrefixes()`; several: the fallback, as today |
| agent mode's `read_full_document` (`cmd_query.go:452-454`) | added only for `*LeannSearcher` | one target: `Service.ReadDocumentTool`; several: not added, as today |

`createAskSearcher`, the REST and MCP chat construction and `batch_ask`'s inline chat config reduce to "decode, `View`, `NewChat`". The VisionRAG block duplicated in `handleAsk` and `handleAskStream` becomes one function in the same file.

**Read and graph tools** borrow a `Handle` with `Service.Acquire(ctx, entry, index)`. It accepts no options and has no search method.

**def** is `Service.Definitions`: acquire, `gleann.FindDefinitions`, release, with `RootDir` set from the config in one place (CLI and MCP set it, the TUI does not; whether that matters was not checked).

### 3.10 The exported Go API

Nothing exported from `pkg/gleann` is removed, renamed or changes its signature, there are no aliases or shims at the public boundary, and no function is added. `NewSearcher`, `Load`, `Search`, `SearchOption` and the 16 `With*` constructors remain the library entry points; the known outside users (`bench/vectordb`, `tests/integration`) use `NewSearcher`, `Load`, `Search` and `With*` options (`bench/vectordb/gleannsearch/gleannsearch.go:119`). Who else imports the package is not known.

Rules:

1. **No method is added to an exported interface** (`Searcher`, `Reranker`, `EmbeddingComputer`, `EmbedderResolver`, `QueryExpander`); that would break outside implementers. New needs get new interfaces in `internal/retrieval` (`Handle`).
2. **Exported identifiers whose behaviour this plan changes or may change**, with the rule that such a change gets a CHANGELOG entry under a "library" heading even when `apidiff` reports nothing:
   - `(*LeannSearcher).Search`: step 16 restructures `search`; closing hole 11 (step 18, decision D22) would apply Kind, MinScore and GraphContext on the lexical-only path for every outside caller.
   - Kept with known defects and no in-repo caller after step 17: `SearchMultiple` (access filtering inside, hole 13), `MultiSearcher`, `LoadMultiSearcher`, `NewMultiSearcher` (fixed merge base, hole 10), `(*LeannSearcher).SetReranker`, `SetQueryExpander`, `(*LeannChat).SetReranker`. They stay as they are because an outside caller may depend on either behaviour; their tests stay in `pkg/gleann` so they do not rot.
3. **Deprecation**: an exported identifier gets a `// Deprecated:` comment only when a public replacement exists and no in-repo caller is left (the local CI runs staticcheck, which reports uses of deprecated identifiers). `internal/retrieval` is not public, so this RFC deprecates nothing. Removal is not done in a v1.x release.
4. **Gate**: `apidiff` between the step 0 commit and each step, for the default, `eif` and `treesitter` tag sets; accepted output is additions only (section 6.4). This is the check `docs/architecture.md:357` already proposes. `apidiff` sees signatures, not behaviour; rule 2 covers what it cannot.

### 3.11 `pkg/gleann`

What the service changes for the package:

1. **Files, not packages.** `searcher.go` (1791 lines) is split inside the package into `searcher_load.go`, `searcher_search.go`, `searcher_rank.go` (`buildBoostedResults`), `searcher_lexical.go`, `index_admin.go` (`ListIndexes`, `GetIndexMeta`, `UpdateIndexMeta`, `RemoveIndex`) and `graphrag.go`. `search` (319 lines) becomes four stages (plan, retrieve, fuse, post-process) in the same order as today. No exported name moves, and the white-box test files are untouched.
2. **Policy gets a home above the library.** Access rules, target expansion, per-surface defaults and caching live in `internal/retrieval`. New code of that kind no longer has to go into `pkg/gleann` to be shared.
3. **One importer of the search internals.** After G1, a later move of search code touches `internal/retrieval`, not five packages.

A package-level split (chat to `internal/llm`, plugins to `internal/plugins`, a leaf package of shared types with aliases, as planned in `docs/architecture.md:311-363`) is not part of this RFC. It does not depend on the service and the service does not need it; its cost is in the type closure of `Config` (12 methods in 6 files, referencing chat types), three unexported members that cross the cut (`SearchResult.pid`, `RerankerConfig.autoCodeModel`, `PassageStore.idSpan`), assignable package variables (`CurrentVersion`, `RegistryURL`) and the white-box tests. It should be its own RFC with `apidiff` as its gate (decision D18).

## 4. The structural tests

Options are enumerated by reflection over the exported fields of `retrieval.Options` (17). Entries are enumerated by `retrieval.Entries()`.

| Test | Where | Fails when |
|---|---|---|
| T1 matrix complete | `internal/retrieval` | for some search entry and some `Options` field there is no cell; or a cell has neither an input nor a non-empty exempt reason; or two inputs of one entry share a name; or a `Requires` field has no cell with an input or default in the same entry; or an entry has no path. Adding a field or an entry without deciding every cell fails here |
| T2 decode | `internal/retrieval` | for some entry, cell and input, with the three sentinels of that input (valid, zero, invalid): the valid value encoded under the input's name in the family's format does not come back in that `Options` field (or in the `Also` field), or sets a field the cell does not name; the zero value is not treated per the presence rule; the invalid value is not rejected, ignored or passed as declared |
| T3 apply | `internal/retrieval` | forward: for some `Options` field and two valid values a and b, `plan(requires + field = a)` and `plan(requires + field = b)` give the same `SearchConfig` and the same instances, on either of two base configs (the zero config, and `DefaultConfig` with every bool inverted). `RerankModel`, `RerankProvider`, `RerankURL` and `ExpandModel` have no effect without their required field, which is why the comparison sets it. Reverse: some field of `gleann.SearchConfig` (18, by reflection) is neither written from an `Options` field nor listed as derived (`CustomReranker`, `Expander`, `RerankerConfig`) or load-time (`MaxBM25Passages`). A new `SearchConfig` field without a request field fails here |
| T4 no bypass | `internal/doccheck` | see below |
| T5 end to end | each surface package, through `retrievaltest.CheckEntries(t, family, drivers)` | for some entry, path and input: driving the real entry point (the inner command function, the HTTP handler, the tool handler) with the valid sentinel on a recording service, the observation does not carry the value in the decoded `Options` and in the planned `SearchConfig`; or the zero and invalid sentinels are not treated as the cell declares; or, for a cell without input, the name is not rejected (CLI, exit status 2) or ignored (REST, MCP) as declared; or a path listed by an entry has no driver |

**T4.** It stays syntactic (`internal/doccheck` parses with go/ast, `doccheck_test.go:8-10`), resolves import aliases, and looks at selector expressions, not only calls, so a function value (`f := gleann.WithTopK`) is seen. Scope: every non-test file of the module except `pkg/gleann`, `internal/retrieval`, `pkg/backends` and the bench modules. It fails on:

- a selector naming `gleann.NewSearcher`, `SearchMultiple`, `LoadMultiSearcher`, `NewMultiSearcher`, `NewReranker`, or any function of `pkg/gleann` whose result type is `SearchOption` (found by parsing `pkg/gleann`, so a new constructor is covered without editing the test);
- the type names `gleann.SearchOption`, `gleann.LeannSearcher`, `gleann.MultiSearcher` (a surface can otherwise write its own option closure, or assert a `Handle` or a `Searcher` back to the concrete type);
- a call of `SetReranker`, `SetScorer` or `SetQueryExpander`;
- an assignment whose left side is a selector through `.SearchConfig.`, outside the config resolver file;
- a composite literal of `retrieval.Options` or `retrieval.Entry`, or a use of `retrieval.WithObserver` or `WithLoader`, in a non-test file outside `internal/retrieval`;
- a package-level variable of type `*retrieval.Service`.

It has an allow-list of (file, identifier, reason), printed by the test. Expected day-one content: `cmd/gleann/cmd_benchmark.go`, which measures the lexical leg with `SearchBM25` on its own searcher (`:87, 145`). Until step 17 it runs in report mode and prints the remaining sites. What it cannot see: a `SearchConfig` field written through a pointer taken earlier (`sc := &config.SearchConfig`), and anything behind reflection. Using type information instead is decision D13.

What the five tests say together: T1 and T2, every option has a name and a value rule on every entry or a written reason. T3, a request field reaches the library config and the library config has no field the request lacks. T4, no surface can search around the service. T5, each handler path passes what it decoded. They stop at `SearchConfig`. Hole 11 is an option that reaches `SearchConfig` and is ignored on one library path; none of the five would catch the next one (decision D16). A hole can still be created by adding an exemption; that is a reviewed line with a reason, and the exempt list is printed by T1.

## 5. Build Tag & Platform Impact

- **Default build (`CGO_ENABLED=0`, no tags):** `internal/retrieval` is plain Go with no build constraints and no new dependency (`golang.org/x/sync` is already used for singleflight and errgroup). No stub files are needed. No Python, no Docker, no new CGo.
- **`eif`:** the in-process reranker is reached as today, through `gleann.NewReranker` and the hook `RegisterEIFRanker`; `ResolveRerankerConfig` keeps choosing the provider. `chat_eif.go` / `chat_no_eif.go` are not touched. Not checked: whether building a reranker per request reloads a model under `eif`, whether a cached reranker or embedder instance holds model memory, and whether an evicted one must be closed. Step 0e measures the resident memory of `gleann mcp` built with `-tags eif` before and after one `rerank: true` call; the bound of 8 in section 3.5 is reviewed against that number before step 7.
- **`faiss`, `native`:** backends come from the registry filled by `pkg/backends`; binaries keep their blank import, `internal/retrieval` tests add it. `backend_default_{faiss,hnsw}.go` are not touched.
- **`treesitter`:** not involved; graph context is read from the index database in every build.
- **Windows:** a retired searcher stays open until its last borrower releases it. Publication over an open index is already handled by the readers (RFC 005 section 5: heap copy, file closed after load). Delete and storage switch need the files closed first; rule (a) of section 3.7 keeps the invalidation before the write and the retry loop of `removeIndexRetrying` (`server.go:1456-1466`) stays in the server. The MCP server moves to the same rule in step 13. No platform-specific call is added.
- **Architectures.** Whether search scores are bit-identical between amd64 and arm64 was not measured. The Go compiler may fuse a multiply and an add on arm64 and not on amd64, which the language specification allows, and that can change the last bits of a dot product and flip a near-tie. The goldens are built for that (section 6.2, step 0b).
- **File moves:** the build-tag pairs in `pkg/gleann` (`chat_*eif*`, `capabilities_*`, `backend_default_*`, `index_usage_*`) are not part of the file split of section 3.11.

## 6. Incremental & Migration Strategy

Precondition: the uncommitted next-release work is committed (seven files of `pkg/gleann` are modified in the working tree on 2026-11-21, among them `searcher.go` and `types.go`).

No index, config or wire format changes, so there is no data migration and old and new binaries can share an index directory.

### 6.1 Order

The first draft moved the cache before any handler stopped wiring options by hand. This revision turns that around: handlers first adopt the decoder and `plan` in place, on the cache they have (steps 4 to 8); the cache, the multi-index code and the MCP cache deletion follow (steps 9 to 14). Reasons: 23 of the 29 CHANGELOG entries of section 1.3 are option, flag or config kinds and need only decoders, `plan` and one resolver; the cache move carries the concurrency and Windows risk; and the order gives a place to stop.

**After step 8** the option and config kinds of section 1.3 are covered by T1, T2, T3 and T5. The steps that follow remove duplication (G2, the multi-index copies) and make T4 enforceable; they add nothing to G4. If the cache move shows a latency or Windows problem, the work can stop after step 8 with two caches left.

The cost: each handler is edited twice, once to call `Plan` and once to call `Search` or `View`. The second edit replaces "acquire, search, release" by one call. Decision D2.

### 6.2 Steps

Each step is one mergeable change. Checks for every step: `gofmt`, `go vet ./...`, staticcheck; `go build ./cmd/gleann` with no tags, `-tags eif`, `-tags treesitter`; `go test -race ./...`; `apidiff` additions only; the goldens of step 0b. The quality gate and the recall gate run on the steps that edit `pkg/gleann` (15, 16, and any step 18 commit that does); both call the library directly and say nothing about the surface moves.

| # | Step | Files | Own acceptance check |
|---|---|---|---|
| 0a | **Fixtures.** `hashEmbedder` moves from `internal/quality/embedder_test.go` to a non-test file. New `internal/paritytest`: one HTTP fake for embeddings, chat and rerank with request counters (today a fake exists only in package main's test files, `cmd/gleann/cli_harness_test.go:47-108`, and no rerank stub exists), and a small corpus with no passage shared between its indexes | new non-test files only under `internal/paritytest` and `internal/quality` | `go test ./internal/quality ./internal/paritytest` |
| 0b | **Goldens.** Per entry and bound input: 5 to 8 queries, plus the edge values of the input (absent, zero or false, negative, malformed); direct `LeannSearcher.Search` over every option; error paths (private index, unknown index, old store format, busy); `tools/list`, the OpenAPI document, `search --help`, `ask --help`. Output: passage ids in rank order and scores rounded to 4 decimals, timing fields removed. Default backend only (HNSW, fixed seed `modules/hnsw/hnsw.go:119`; the DiskANN build is not deterministic per `bench/vectordb/gate/thresholds.go:21-22`) | new test files | identical over `-count=3` with `GOMAXPROCS` above 1 on Linux, macOS and Windows CI. A query whose rank order differs between platforms is removed from the set and listed. Full-precision comparison is done before and after on one platform only. The step reports its wall time under `-race` on the hosted Linux runner; if it adds more than 2 minutes (proposed threshold), the goldens get their own CI step without `-race`, as the quality gate has (`ci.yml:54-58`) |
| 0c | **Hole tests.** A table "hole, observable, harness" and one characterising test per hole that has an observable today: hole 1, the rerank stub receives N candidates; holes 3, 4, 6, the rerank stub's request count is 0; holes 9, 10, 13, a multi-index result; holes 17, 18, the endpoint and key the fake received after a config change. Hole 2 has no observable without the recorder and hole 16 is a data race with no green characterising test: both are written in the step that closes them and marked so | new test files | every hole of section 1.4 has a test or a named later step |
| 0d | **Config goldens.** The resolved `Config` as JSON per (command, saved file, environment, flags), including the config `mcp.NewServer` builds. `gleann.Config` holds functions and interfaces, so the comparison is on the JSON form | new test files in `cmd/gleann` | recorded values match appendix B, or appendix B is corrected |
| 0e | **Baselines.** `apidiff` as a CI step (section 6.4); latency baseline (section 6.5); `EstimatedSize` of the quality-corpus index and of one 100k-passage index from the scale driver; the `eif` memory measurement of section 5 | `.github/workflows/ci.yml` | numbers recorded in this RFC's audit log |
| 1 | `retrieval.Options`, cells, entries, decoders; T1, T2 | new `internal/retrieval` | T1, T2 green. The bound inputs are those of section 1.2 as R, restated per field (appendix A completed to 17 x 14 and reviewed against the handlers), plus the two MCP `rerank` arguments; the `Dropped` names are the four ✗ cells and REST `ef_search`, `recompute_embeddings` |
| 2 | `plan`, the instance maps, `New`, `Plan`, the observer and loader options, `retrievaltest`; `embedding.FromConfig`; T3. Pure functions and a service with no cache and no caller | `internal/retrieval`, `internal/retrieval/retrievaltest`, `internal/embedding`, `internal/indexing/env.go` | T3 green; unit tests of section 7 |
| 3 | One `resolveConfig(args, profile)` for the CLI commands, `cmdChat` and the TUI. `runMCPServer` keeps its config until step 13 | `cmd/gleann/config.go`, `cmd_query.go:537-600`, `cmd_serve.go`, `internal/tui/tui.go` | the config goldens of 0d are identical for every command, including chat and the TUI |
| 4 | CLI in place: `cmdSearch` and `cmdAsk` get inner functions that receive the service; options come from `FromArgs` and `Plan`; `--top-k` and `--hybrid` leave the config path for these two commands; flag sets derived from the cells; T5 for the CLI family | `cmd/gleann/cmd_{search,query}.go`, `flagspec.go`, `expand_flags.go` | CLI goldens identical; `command_flags_test.go` green (`TestHelpFlagsAreAccepted` ties help text to the accepted set); T5 CLI |
| 5 | REST in place: `handleSearch`, `handleMultiSearch`, `handleAsk`, `handleAskStream` decode with `FromJSON` and call `Plan`, on the present cache; OpenAPI option properties from the cells; T5 for the JSON family, both ask paths | `internal/server/server.go`, `multi_search_handler.go`, `openapi_schemas.go` | REST goldens identical, including the OpenAPI document; T5 REST |
| 6 | Proxy in place, both modes, stream and non-stream paths; A2A gets its entry (no inputs) | `internal/server/proxy_*.go`, `a2a_handler.go` | proxy goldens (context block, `X-Gleann-Skipped-Indexes`) and A2A golden identical; T5 for the Header family |
| 7 | MCP in place: search, search_ids, search_multi, ask, batch_ask decode with `FromMCP` and call `Plan`, on the MCP cache; tool schema option properties from the cells; T5 for the MCP family | `internal/mcp` | MCP goldens identical, including `tools/list`; test of hole 6 updated for the one named change of this step (section 6.3) |
| 8 | TUI in place: the two chat paths build `Options` through the session setters; T5 for the Session family | `internal/tui/chat.go`, `agentchat.go` | TUI ask-path tests green |
| 9 | Cache as a new type in `internal/retrieval`, with per-index generations; `Search` for one index, `Acquire`, `Handle`. The REST server's `acquireSearcher`, `invalidateSearcher` and `invalidateAllSearchers` become thin calls into its service. Preceded by one mechanical commit that rewrites the test literals (`searchers:` in 137 literals, 27 files) and the tests that read cache fields (`searcher_cache_size_test.go`, `security_test.go`, 11 lines indexing `.searchers[`) to `retrievaltest.Preloaded` | `internal/retrieval/cache.go`, `internal/server/searcher_cache.go`, `server.go`, the listed test files | REST goldens identical; cache unit tests of section 7; `go test -race ./internal/server ./internal/retrieval`; latency check |
| 10 | Targets, access policies, fusion rules and multi-index `Search` in the service; `View`; no caller yet | `internal/retrieval` | differential test (section 7); access table tests |
| 11 | CLI onto `Search` and `View`: `cmdSearch`, `cmdAsk`/`createAskSearcher`, `cmdDef`, `runContextBenchmark` | `cmd/gleann/cmd_{search,query,def,benchmark}.go` | CLI goldens identical, including `ask --cite` on an index with prefixes and a missing index; tests of holes 1, 10, 13 unchanged unless a hole is closed here by decision |
| 12 | REST onto `Search` and `View`: search, `/api/search`, ask, ask-stream, `recallVector`, proxy, A2A (its loop over one-target searches). Waits for RFC 007 if that RFC edits `a2a_handler.go`, or the reverse | `internal/server` | REST, proxy and A2A goldens identical; `-race` test with concurrent asks during a publication (hole 16); tests of holes 3, 4 unchanged unless closed by decision; holes 17, 18 closed here (section 6.3) |
| 13 | MCP onto the service: MCP cache deleted; the 19 acquire sites use `Acquire`, `Search` or `View` with their entries; `runMCPServer` uses `resolveConfig` with the MCP profile in the same commit; roots change calls `InvalidateAll`; `graphPool` registers `OnInvalidate`; `ErrBusy` added to the busy test | `internal/mcp`, `cmd/gleann/cmd_mcp.go` | MCP goldens identical; for every MCP tool that takes an index: a private index and an index outside `GLEANN_TAGS` are refused with today's message, and an empty index resolves as today; `go test -race ./internal/mcp`; tests of holes 8, 9 per decision |
| 14 | TUI onto `View`: `SearcherLoader` and `tui.loadConfig` removed; the rerank toggle rebuilds the view; scope switch invalidates the indexes that left | `internal/tui`, `cmd_query.go:600-692` | TUI tests: the rerank toggle changes the recorded `SearchConfig`; `/graph` and @-mention on a one-index view; test of hole 12 unchanged unless closed by decision |
| 15 | `searcher.go` split into files, no code change | `pkg/gleann` | `go doc -all ./pkg/gleann` identical before and after; `apidiff` empty; both gates green |
| 16 | `search` into stages, same order of operations | `pkg/gleann/searcher_*.go` | library goldens identical at full precision on one platform; `go test -count=1 -run TestRetrievalQuality ./internal/quality -update && git diff --exit-code internal/quality/testdata/baseline.json` (equal, not merely within the 0.01 tolerance); latency check |
| 17 | T4 switched from report to fail; dead wiring removed; `server.go` (REST) split by resource and `server.go` (MCP) by tool group as commits without code change | surfaces | T4 reports only the allow-list; T5 green for every family |
| 18 | Close exemptions and holes, one commit each: change the cell or policy, add the parity case, CHANGELOG line. Which ones: decisions D1, D4-D8, D19-D23 | `entries.go`, one test, sometimes one surface file | T1-T5; all earlier goldens unchanged except the named case |
| 19 | Docs: package table and "Planned refactors" in `docs/architecture.md`; API, MCP and environment docs (including the two descriptions of a cache budget of 0, `types.go:323-326` against `docs/env-vars.md:87`); ROADMAP W6 | `docs/` | `internal/doccheck` green |

### 6.3 Changes of behaviour made by the move itself

Everything else is identical for the same request bytes. Each of these has a test from step 0c or its own step, and a CHANGELOG line.

| Step | Change | Why it cannot be avoided |
|---|---|---|
| 7 | MCP `rerank: true` reranks without `GLEANN_RERANK` having been set at load (part of hole 6) | `plan` builds the reranker when the effective value is true; reproducing "true, but only if an environment variable was set when the searcher loaded" would mean writing the defect into the cell |
| 9, 12, 13 | a search that hits `ErrIndexChanged` is retried once on every entry point, where today some return the error | one retry rule in the service |
| 12 | holes 17 and 18: after a config change the per-index embedders and the expander use the new endpoint and key | the service's instance maps are keyed by endpoint and credential and cleared by `InvalidateAll` |
| 12, 13 | `/api/search` and `gleann_search_multi` keep their searchers cached; memory use of a server that answers multi-index queries rises (section 8) | they move onto the cache |
| 13 | an MCP roots change also drops the graph handles of the old directory | `OnInvalidate` |
| 11, 12 | on the REST single-index routes and in `gleann search a,b`, a `GLEANN_TAGS` value with nothing but empty elements (for example `,`) refuses every index, where today it is treated as unset. No other value changes: `IndexMeta.HasTag` already trims (`types.go:179-188`), so the MCP server's untrimmed elements match the same indexes | one parser, and the stricter of the two existing rules for a degenerate value |
| 13 | on MCP, an access, name or target error for an index whose name contains `lock`, `timeout`, `bbolt` or `status 1` is answered with its own message instead of "is currently being built or synced" | the service's sentinels are checked before the substring test (section 3.3.2) |

### 6.4 Lanes and overlaps

Steps 0 to 3 are serial. Steps 4 to 8 are one per surface and can run beside each other with these overlaps, which are real and were not in the first draft's "disjoint files":

- `internal/retrieval/entries.go` and `CHANGELOG.md` are edited by every step that touches a cell; such commits are rebased serially.
- `cmd_query.go` is edited by the CLI steps and the TUI steps; 8 follows 4, and 14 follows 11.
- 31 test sites in `internal/mcp` and `internal/server` set `GLEANN_TAGS` with `t.Setenv` and call handlers; they keep working because the value is read per call (section 3.6).
- `internal/server/a2a_handler.go` is edited by RFC 007 of the same wave (only with its decision D2); step 12 waits for it or the reverse.
- `pkg/gleann` is edited only by steps 15, 16 and possibly 18. Steps 15 and 16 run before step 4 starts or after step 14, never beside the surface steps (`docs/architecture.md:340` asks for a quiet window too).

Steps 9 and 10 are serial. Steps 11 to 14 are one per surface with the same overlaps. Steps 17 to 19 are serial. WORKPLAN marks WP-5.1 "run alone"; run serially, the order is the table's. Decision D24.

### 6.5 Commands

API (not run for this RFC: `apidiff` and `benchstat` are not installed on this machine and no workflow step runs `apidiff` today):

```
go install golang.org/x/exp/cmd/apidiff@<version pinned in step 0e>
# in a worktree of the step 0 commit:
apidiff -w "$TMP/base.export" ./pkg/gleann
# in the step:
apidiff -incompatible "$TMP/base.export" ./pkg/gleann      # must print nothing
```

The export file is binary and is not committed (AGENTS.md 4.2); CI regenerates it from the step 0 tag. The three tag sets are run with `GOFLAGS=-tags=<set>`; that `apidiff` honours it is confirmed in step 0e.

Latency, black box, through `gleann serve` and the CLI, with the driver that produced the v1.20 numbers quoted in RFC 006 section 1:

```
cd ../gleann-rag-test
go run ./scale -bin <gleann binary> -src <checkout of the step 0 commit> -sizes 10000,100000 \
    -queries 300 -skip-sync -label <step> -out results/scale/rfc008.jsonl
```

`-src` defaults to the working tree (`scale/main.go:734`), so it is pinned; otherwise baseline and step index different text. At least 10 runs per binary. The check is meant to detect a regression of 10 % of warm p50 (proposed value); a step fails when its median is above the baseline median plus that margin, compared with benchstat on the per-query samples. The first draft's rule (median of three above the maximum of three) fails about one comparison in five under noise alone: for six exchangeable samples the two largest are both from the step with probability (3/6)(2/5) = 0.2.

The driver measures `gleann search` and `POST /api/indexes/{name}/search` only (`scale/main.go:433, 645`). It has no MCP, ask or multi-index path, so the latency of those is not checked black box by this RFC (decision D15). Library level: `go test -run '^$' -bench BenchmarkFilteredSearch -count 10 ./pkg/gleann` for steps 15 and 16, and a new `BenchmarkServiceSearch` in `internal/retrieval` (step 9) for the cost of `plan` and the cache over a direct `Search`. No number exists yet: the machine was running long benchmarks when the draft and the audit were written.

## 7. Test & Verification Matrix

| Layer | Test | Step |
|---|---|---|
| unit, `internal/retrieval` | T1, T2, T3; `plan` precedence (request by presence rule, cell default, config) per field; `plan` rejects a config with `CustomReranker` or `Expander`; the zero `Entry` is rejected by every service method; an entry with a tags policy fails closed when `AllowedTags` is nil; `New` with a nil `Config` returns an error | 1, 2 |
| unit, instance maps | reranker and expander reused per key; 100 distinct `rerank_model` values leave at most the bound in the map; after `InvalidateAll` with a changed OpenAI key the next expander and embedder use the new key | 2 |
| unit, cache | singleflight under concurrent acquire; `Invalidate("a")` during a load of "b" does not discard "b"; `InvalidateAll` during a load discards it; delta-only change absorbed; retire while borrowed closes on the last release; budget and count eviction; `OnInvalidate` fires once | 9 |
| unit, targets and access | `@tag`, `@all`, code/docs pair; every row of the access table of section 3.8, including unreadable metadata and the refused-index text; client target mismatch; a `mcp_exposed` change on disk applies to the next request without a reload; skipped indexes in `Response.Skipped` | 10 |
| differential | `Service.Search` against `SearchMultiple` on every multi-index case of 0b; against `MultiSearcher.Search` only where the options pass top-k and alpha explicitly or the config has top-k 10 and alpha 0.7 (the two functions fuse from different bases, hole 10); all indexes exposed; ties ordered by (index, id) on both sides. The cases where the two functions differ (config alpha other than 0.7, top-k above 10, a private index) are listed with the result the service must give | 10 |
| parity | goldens per entry, input and edge value; library goldens over every option; `tools/list`, OpenAPI, `--help` | 0b, every step |
| characterisation | one test per hole of section 1.4, updated by the step that closes it | 0c, 7, 12, 13, 18 |
| config | resolved-config goldens per command | 0d, 3, 13 |
| structural | T4 (`internal/doccheck`) in report mode, then fail mode; T5 per family and path | 4-8, 17 |
| access on handles | for every MCP tool that takes an index: private index refused, index outside `GLEANN_TAGS` refused, empty index resolved (`GLEANN_INDEX`, working directory, single exposed index), each with today's message | 13 |
| views | TUI rerank toggle changes the recorded `SearchConfig`; `ask --cite` golden on an index built with query and passage prefixes; `/graph` and @-mention on a one-index view; missing index gives the same message and exit status on `ask`; `Metadata["_index"]` present over two targets and absent over one; `LeannChat.SetReranker` on a view is a no-op (documents the library behaviour) | 11, 14 |
| cross-process | `serve` or `mcp` running, a CLI sync in between: the next search is correct on every entry point; `ErrIndexChanged` retried once | 9, 13 |
| race | `-race` on `internal/retrieval`, `internal/server`, `internal/mcp`; `batch_ask` with 5 goroutines during a publication (hole 16) | 9, 12, 13 |
| quality | `TestRetrievalQuality` (BM25, Dense, Hybrid against `baseline.json`) | 15, 16, 18 when `pkg/gleann` is edited |
| recall | `make recall-gate` (`Makefile:417-419`, 20 minute limit) | 15, 16, 18 when `pkg/gleann` is edited |
| API | `apidiff` on `pkg/gleann` for three tag sets; `bench/vectordb` builds against the tree | every step |
| build tags, platforms | default with `CGO_ENABLED=0`, `eif`, `treesitter`, `faiss` where libfaiss exists; Linux, macOS, Windows CI | every step |
| latency | `scale` and the benchmarks of section 6.5 | 9, 11, 12, 16 |

## 8. Risks

- **Step 0 carries G5.** Anything the goldens do not cover can change unnoticed. The coverage of 0b (which inputs, which edge values, which error paths) is the main thing to review before step 1. Its size is an estimate, not a measurement: about 64 bound inputs times 5 to 8 queries plus the edge cases, on a small corpus.
- **Memory in long-lived servers.** `/api/search` and `gleann_search_multi` open and close a searcher per index per call today (`multi_search.go:108-114`); through the cache those searchers stay loaded. The REST cache has no count cap and its byte budget is unlimited unless set; the budget is compared with file sizes on disk, so a byte default would evict memory-mapped indexes that cost little resident memory. A query over all indexes would keep all of them cached. No size per index is quoted here; step 0e records `EstimatedSize` for two indexes. A default guard is needed before step 12 (decision D9). The CLI one-shot commands set no cap; the TUI keeps today's behaviour by invalidating on a scope switch.
- **A fix hidden in a move.** Some holes close by construction when a surface moves (6 in part, 8, 16, 17, 18). Section 6.3 names them; anything not named there and not covered by step 0 is a defect of this plan.
- **Unread code.** Not read for the draft or the audit: `proxy_cover.go` beyond its headers, MCP `focusResults`, the memory pools, most TUI handlers, the web UI, `rerankerConfigFromFlags`, `buildChatConfig`, MCP `queryExpander`, the whole of REST `handleAsk` beyond the cited ranges. Behaviour there is protected only by goldens.
- **`--no-hybrid`.** It sets alpha 1.0 and also calls `SetScorer(nil)` on the single-index searcher (`cmd_search.go:75-77, 102-103`). A cached searcher cannot be mutated per request. Whether alpha 1.0 alone gives the same bytes is not known; the step 0b golden decides, and if it does not, "no lexical leg" becomes a field of `Options` in step 11 with its own cell row.
- **Golden stability.** Cross-index fusion ranks by a stable sort over the input order (`fuse.go:50-54, 63-66`) and both library functions append parts in completion order (`multi_search.go:121-123`, `multi_searcher.go:119-121`). When two hits of different indexes have the same raw distance or BM25 value, their ranks, and so their fused scores, depend on which index finished first; ordering equal scores in the output does not remove that. The golden corpus therefore has no passage shared between indexes, and the service collects parts in name order.
- **Platforms.** Scores may differ in the last bits between architectures (section 5); the goldens compare ids in rank order and rounded scores, and drop a query that differs.
- **Two lists of options.** The cells are a second place to edit next to `SearchConfig`. T3's reverse check ties them.
- **Each handler is edited twice** (section 6.1).
- **Test rewrite.** Step 9 rewrites 137 struct literals in 27 test files mechanically. It is one commit without a production change, but it is large.
- **Concurrent edits.** `pkg/gleann/searcher.go` and `types.go` have uncommitted changes; step 15 needs a quiet window.
- **Size.** 9 construction sites, 27 acquire sites, 14 search entries, about 20 steps. Not estimated in lines or days.

## 9. Alternatives considered

- **Do nothing but add the structural test.** A reflection test over `SearchConfig` against each surface's request type. Cheap, and it would have caught the "–" cells. It cannot work as stated on the CLI, MCP and proxy, which have no request struct (flag scans, map lookups, header reads), so it needs a hand-kept table per surface; that table says a name is declared, not that it is read (the 4 ✗ cells are declared names) or that a reranker instance exists behind the flag (holes 3, 4, 6). Checking behaviour needs a recorder at every construction site. Steps 0 to 8 of this plan are the worked-out form of this alternative: cells, decoders, `plan` and a recorder, with both caches left alone. They have value on their own and are the stopping point of section 6.1.
- **Generate the per-surface decoders** (`go generate` from one spec: flag parser, JSON struct, MCP schema, header parser, docs). Gives the same guarantee at compile time and can emit docs. Costs a generator to maintain and generated code in every review; the CLI parser is hand-rolled (`flagspec.go`), so the generator would target four unlike shapes. The cells are the spec a generator would need, so this stays possible.
- **Put the service in `pkg/gleann`.** It cannot construct embedders there (import cycle with `internal/embedding`) and it would add policy to the package this RFC tries to stop growing.
- **Make the service public now (`pkg/retrieval`).** It would freeze `Request` before one release of use. Decision D18.
- **Keep both caches and share a generic helper.** Removes the duplicated cache code (not counted) and none of the option wiring. After step 8 this is what the tree looks like if the work stops there, without the helper.
- **Move the cache first** (the first draft's order). One edit per handler instead of two; the concurrency and Windows risk comes before any of the bug class is covered. Decision D2.
- **`Acquire` returns `*gleann.LeannSearcher`** and T4 forbids `.Search` on it. T4 is syntactic and cannot tell that `Search` from the memory and store `Search` methods without type information. The narrow interface makes the same thing a compile error. Decision D3.
- **Split `pkg/gleann` first.** Independent of the bug class and the larger risk; see section 3.11.

## 10. Decisions for the maintainer

### Decided by this RFC

The rest of the text already assumes an answer for these 12 decisions:

- **D2 (Execution order):** Handlers adopt decode and `plan` in place before cache unification (steps 4 to 8 in place, steps 9 to 14 cache). Relied on by sections 6.1 and 6.2.
- **D3 (`Acquire` return type):** The narrow `Handle` interface without a `Search` method. Relied on by sections 3.3, 3.9 and 4 (T4).
- **D8 (A2A multi-index logic):** Keep the sequential, unfused loop over one-target searches. Relied on by section 3.8 and section 6.2 (step 12).
- **D10 (CLI error exit handling):** `os.Exit` on CLI error paths with the service not closed is kept as today's behaviour. Relied on by section 3.3.1.
- **D11 (Isolation tags location):** `retrieval.Deps.AllowedTags` (read per call), not in `gleann.Config`. Relied on by sections 3.3 and 3.6.
- **D13 (T4 implementation):** Syntactic AST check in `internal/doccheck` without `go/packages`. Relied on by section 4.
- **D14 (Derived tool descriptions):** Description per input, byte-identical to today. Relied on by section 3.4.
- **D15 (Black-box latency check scope):** Black-box latency check covers CLI search and REST search only; MCP and ask excluded. Relied on by section 6.5.
- **D17 (Incremental sync cache reload):** Keep today's behaviour: drop and reload searcher on in-process incremental sync. Relied on by section 3.7.
- **D18 (Package placement & RFC 009 split):** `internal/retrieval` package boundary; package-level split of `pkg/gleann` deferred to RFC 009. Relied on by sections 3.1 and 3.11.
- **D20 (Frozen surface feature freeze):** Frozen surfaces stay exempt; no new inputs or features added. Relied on by section 2 and section 3.4.
- **D25 (Deprecation rules):** Rules 1 to 4 of section 3.10 accepted (no deprecation without public replacement, nothing removed in v1.x, `apidiff` in CI, CHANGELOG heading for behaviour changes). Relied on by section 3.10.

### Open decisions

The text above is written with the recommended answer. Each decision changes only the places named.

| # | Question | Recommendation | Affects |
|---|---|---|---|
| D1 | Should an explicit zero or false become meaningful on REST and MCP: `{"rerank": false}` or `{"expand": false}` overriding a saved setting, MCP `top_k: 0` giving the entry's default? | Not in this RFC. Every cell keeps today's rule. A client that serialises its struct without omitempty sends `"rerank": false` today and is reranked by a saved setting; changing that is a wire change with no bug report behind it. If wanted, one step 18 commit per cell | 3.4, appendix A, step 18 |
| D4 | `GLEANN_RERANK=1` on the MCP server: documented as "rerank even when the config is off" (`docs/env-vars.md:44`), implemented as "a reranker is available when the request asks" (`mcp/server.go:484-493, 953-955`). After step 7 a request can ask without it, so the code meaning leaves the variable with no effect | The documented meaning, limited to the two tools that have a `rerank` argument (`gleann_search`, `gleann_search_ids`): the variable makes the cell's default true. Step 18 commit with a CHANGELOG line; it changes results and adds a reranker call per query for whoever set the variable. Alternative: no effect, and fix the doc | 3.6, steps 13, 18 |
| D5 | Do the saved `rerank_enabled`, `expand` and `top_k` become defaults for MCP searches? `CHANGELOG.md:206` says `rerank_enabled` already applies to MCP; it does not | `rerank_enabled`: yes for the two tools with a `rerank` argument, as a step 18 commit (the changelog already promises it). `expand`: no; it adds an LLM call of 1 to 3 s to every agent query. `top_k`: no; the entry defaults of 5 and 10 stay (D21) | 3.6, appendix B, step 18 |
| D6 | Should the MCP server honour `GLEANN_INDEX_DIR` and `gleann chat` the saved `index_dir` when they move to the one resolver? Today neither does; `docs/env-vars.md:11` documents the variable without a surface limit. The change alters which indexes those processes see | Yes for both, each as a named change in step 18 with a CHANGELOG line. Until step 18, the profiles keep today's values | 3.6, appendix B, step 18 |
| D7 | REST single-index search and ask do not check `mcp_exposed` (hole 21); the web UI can search a private index | Keep per entry. The flag is named for MCP exposure and the web UI is the owner's own view; aligning would hide private indexes from the UI's search | 3.8 |
| D9 | Cache limits before step 12. Today REST has no count cap and an unlimited byte budget, MCP a count cap of 16 | A count cap of 16 for `serve` and the MCP server, the byte budget optional as today, none for the CLI. The byte budget measures file sizes on disk, so it is a poor default guard | 3.7, 8, step 12 |
| D12 | Where do the parity goldens live: in the tree (`internal/paritytest`, run by `go test` and CI), or in the sibling E2E repository next to PAR-01..06, which drives the real binary? | In the tree. PAR-01, 02, 03, 06 stay as black-box E2E; PAR-04 and PAR-05 are covered by step 0b | step 0 |
| D16 | A library-level test T6 in `pkg/gleann`: for every `SearchConfig` field and each retrieval path (hybrid, vector only, lexical only, no embedder) a query for which changing the field changes the result, or an exempt line with a reason (day one: hole 11) | Yes, as a test-only commit in the LIB lane before step 16. Without it the structural tests stop at `SearchConfig`, as section 4 says. Not counted in G4 until agreed | 4, step 16 |
| D19 | Access alignment: should `gleann search a,b` apply `mcp_exposed` and `GLEANN_TAGS` (hole 13; the local user owns every index)? Should the proxy apply `GLEANN_TAGS` (hole 20)? | CLI: no, stop filtering there (step 18). Proxy: yes if the tags are meant as an isolation boundary for every server route (step 18); the proxy is frozen, so this needs an explicit yes | 3.8, step 18 |
| D21 | Default top-k is 5 on MCP search, batch_ask, def and the ReAct tool, 10 elsewhere. Keep per entry? | Keep | 1.6 |
| D22 | Lexical-only path (hole 11): apply Kind, MinScore and GraphContext there (honouring an option), and measure test demotion and the symbol boosts separately (a ranking change for every lexical query)? It changes `(*LeannSearcher).Search` for outside callers | The first three in step 18 with a "library" CHANGELOG entry; the rest is a separate measurement | 3.10, step 18 |
| D23 | Inputs that are accepted and dropped: REST `ef_search`, `recompute_embeddings`; CLI search `--top`, `--base-url`, `--api-key`; hole 19 (`--hybrid` with a non-number). Implement, reject, or keep accepting? `RerankSkipIdentifiers` and `OnePerFile`: bind on REST and MCP, or exempt with the reason "config only"? Both names of an option (`filters`, `metadata_filters`) on both REST and MCP? | Keep accepting the dropped names and remove the two REST fields from the OpenAPI schema; hole 19: ignore an unparsable value, as `--hybrid-alpha` does; exempt the two options; each surface keeps its own filter name. All step 18 or later | 3.4, step 18 |
| D24 | Parallel lanes with the overlaps of section 6.4, or strictly serial as WORKPLAN says? | Serial unless more than one agent lane is wanted; the overlaps are listed either way | 6.4 |

## 11. Audit

- 2026-11-20: first draft, written by a read-only subagent from two source maps of the working tree. Nothing was built, run or measured.
- 2026-11-20: audited from two lenses, invariants and compatibility (19 findings) and plan and tests (16 findings): 4 blockers, 18 majors, 11 minors, 2 questions. The record with every finding, the evidence checked and its disposition is `gleann-review/progress/wave-2026-11-20/008-audit.md`.
- 2026-11-21: all 4 blockers and all 18 majors were checked against the working tree before being accepted. None was found wrong as a whole. Two carried a wrong detail: an explicitly named private index on `/api/search` is refused with 403, not answered with an empty result (`multi_search_handler.go:162-170`); and the count of 77 `NewServer(` calls in `internal/server` tests includes 55 `httptest.NewServer` calls, the package's own constructor is called 21 times. Where the two auditors disagreed (what `Acquire` returns, the scope of T4, how tests reach the cache, who builds the service), the choice and its reason are in the audit record under "Disagreements".
- What the audit confirmed in the draft: no step breaks AGENTS.md section 1 (no Python, no Docker, no new CGo, no new build-tagged file); the import claim of 3.1; no exported identifier of `pkg/gleann` moves, is renamed or changes signature; the counts of 16 `With*` constructors, 19 MCP and 8 REST `acquireSearcher` sites, 9 `NewSearcher` sites in the surfaces; the arithmetic of the matrix (64 R, 4 ✗, 4 E, 2 C, 122 unreachable of 196); the scale driver's flags; that the draft says what was not run.
- What changed, by section:
  - Header, 1.1, 1.2, 1.3, 1.5: the access-check row points to the per-entry table; the 14-rows-against-17-fields note; test construction counts; the PAR tests exist in the sibling repository.
  - 1.4: holes 17 to 21 (stale embedders and expander after a config change, `--hybrid` with a non-number, proxy and `GLEANN_TAGS`, REST single-index search and `mcp_exposed`); hole 6 states both documents that disagree with the code.
  - New 1.6: value rules, invalid-value handling and defaults per entry point.
  - 2: G5 on request bytes; G1's list extended; G4 states where the tests stop; a non-goal for value semantics.
  - 3.2, 3.3: `Request.ClientTarget` and `Path`; `Deps` gains `ChatConfig`, `AllowedTags`, `DefaultIndex`, `CacheBudget`; `New` returns an error; `Acquire` and `Definitions` take the entry; `Acquire` returns `Handle`; `View` takes a context and returns an error; `ReadDocumentTool`; new 3.3.1 ownership and 3.3.2 errors; the write-side embedder is left alone.
  - 3.4: cells keyed by (entry, field) with inputs, presence rule, invalid policy, default source and requirements, replacing the family-level table; entries behind functions, zero entry rejected, zero policy most restrictive; session family, handle entries, fixed callers; `--top-k` and `--hybrid` leave the config path for search and ask; a description per input.
  - 3.5: three precedence levels instead of four; `plan` returns a complete `SearchConfig`; bounded, credential-keyed instance maps owned by the service.
  - New 3.6: resolver profiles; where `GLEANN_TAGS`, `GLEANN_INDEX`, the cache budget and `GLEANN_RERANK*` live.
  - 3.7: generation per index; invalidation before and after a write; budget unit; TUI and CLI behaviour.
  - 3.8: access, default-target, fusion and failure tables per entry; A2A keeps its loop.
  - 3.9: the five type assertions and their replacements; what `View` and `View.Close` do; `_index`.
  - 3.10: rules for exported interfaces, behaviour changes, tag sets.
  - 4: T2 sentinels from the cell; T3 with requirements and two base configs; T4 scope, forbidden set, allow-list and blind spots; T5 on the decoded options, per path, with the recorder that 3.3 now provides.
  - 6: order turned around (in place first); step 0 split in five; the cache step split from `plan`; MCP config moved to the MCP cache step; section 6.3 names every behaviour change of the move; overlaps instead of "disjoint files"; runnable commands; the latency rule.
  - 7, 8: matrix rows for the above; memory, golden and platform risks restated with what is not measured.
  - 10: 25 decisions with recommendations; three appendices.
- Corrected against the tree while checking: the draft said the DiskANN recall threshold is a placeholder (`gateDiskANN = 0.0`); on 2026-11-21 the working tree has `gateDiskANN = 0.98` (`bench/vectordb/gate/thresholds.go:25`). The working tree changed between the draft and the audit, so line numbers not touched by the audit may be off by a few lines.
- Not verified by anyone: the 29 CHANGELOG entries beyond the five re-read; the function line counts of section 1.5; the TUI and `batch_ask` rows of the matrix beyond the lines cited in appendix A; holes 5, 7, 8 and 12 beyond their cited lines; `apidiff` flags and its handling of build tags (the tool is not installed here); whether scores are bit-identical across architectures; the size or wall time of step 0; memory per cached index; anything under `-tags eif`. Nothing was built, run or measured for the draft or the audit; no latency or memory number in this document is a measurement.

## Appendix A. Cells read for this revision

Each line is an input as the handler treats it today; it is the day-one content of the cell. P = presence counts, NZ = only a non-zero, true or non-empty value counts. Entries and fields not listed were not re-read and are filled in step 1.

| Entry | Field | Input | Presence | Invalid | Default | Source |
|---|---|---|---|---|---|---|
| REST search | TopK | `top_k` | NZ (above 0) | – | config | `server.go:814` |
| | HybridAlpha | `hybrid_alpha` | P | reject 400 | config | `:799, 817` |
| | MinScore | `min_score` | NZ (above 0) | – | config | `:820` |
| | Filters | `metadata_filters` | NZ | – | config | `:823` |
| | FilterLogic | `filter_logic` | NZ | pass | config | `:826` |
| | GraphContext | `graph_context` | NZ | – | config | `:829` |
| | IncludeTests | `include_tests` | NZ | – | config | `:832` |
| | Kind | `kind` | NZ | pass | config | `:835` |
| | Rerank | `rerank` | NZ | – | config | `:840` |
| | RerankModel | `rerank_model` | NZ; requires Rerank | – | config | `:841` |
| | RerankPool | `rerank_pool` | NZ (above 0); requires Rerank | – | config | `:843` |
| | Expand | `expand` | NZ | – | config | `:848` |
| | (dropped) | `ef_search`, `recompute_embeddings` | | | | `:734-735` |
| REST `/api/search` | TopK, MinScore | `top_k`, `min_score` | NZ | – | config | `multi_search_handler.go:205, 211` |
| | HybridAlpha | `hybrid_alpha` | P | reject 400 | config | `:22, 56, 208` |
| | Filters, FilterLogic | `metadata_filters`, `filter_logic` | NZ | pass | config | `:214-219` |
| | IncludeTests, Kind | `include_tests`, `kind` | NZ | pass | config | `:223-228` |
| | Rerank | `rerank` | NZ; sets the flag, no instance (✗) | – | off | `:220-222` |
| | (dropped) | `rerank_model` | | | | `:25` |
| REST ask | TopK | `top_k` | NZ | – | config | `server.go:1005` |
| | Expand | `expand` | NZ | – | config | `:1008` |
| Proxy, both modes | TopK | `X-Gleann-Top-K` | NZ (above 0) | ignore | config top-k if above 0 | `proxy_handler.go:165-171`, `proxy_passthrough.go:132-138` |
| | MinScore | `X-Gleann-Min-Score` | P (0 and negatives count) | ignore | config | `proxy_handler.go:172-176` |
| | HybridAlpha | `X-Gleann-Hybrid-Alpha` | P | ignore | config | `:462-472` |
| Proxy pass-through | Rerank, RerankModel | `X-Gleann-Rerank` | on for any value except empty, `false`, `0`; a value other than `true`, `1` is also the model | – | off (config not consulted, hole 5) | `proxy_passthrough.go:147-152` |
| | RerankPool | `X-Gleann-Rerank-Pool` | NZ; requires Rerank | ignore | config | `:153-155` |
| MCP `gleann_search` | TopK | `top_k` | P (0 and negatives go to the library's 10) | ignore wrong type | const 5 | `mcp/server.go:934-937` |
| | Filters | `filters` | NZ | malformed elements skipped | config | `:878-900, 940` |
| | FilterLogic | `filter_logic` | `and` or `or`; sent only with filters | anything else is `and` | const `and` | `:872-876, 942` |
| | GraphContext | `graph_context` | NZ | ignore | config | `:944` |
| | IncludeTests | `include_tests` | P | ignore | config | `:947` |
| | Kind | `kind` | NZ | pass | config | `:950` |
| | Rerank | `rerank` | NZ; works only with `GLEANN_RERANK` at load (E) | ignore | config | `:484-493, 953` |
| | Expand | `expand` | NZ | ignore | config | `:956` |
| MCP `gleann_search_ids` | TopK | `top_k` | P | ignore | const 10 | `search_progressive.go:105-108` |
| | Filters, FilterLogic, IncludeTests, Kind, Rerank | as `gleann_search` | | | | `:117-129` |
| MCP `gleann_search_multi` | TopK | `top_k` | NZ (above 0) | ignore | config | `multi_search.go:59` |
| | IncludeTests | `include_tests` | P | ignore | config | `:62` |
| | Kind | `kind` | NZ | pass | config | `:65` |
| MCP `gleann_ask` | Filters, FilterLogic | `filters`, `filter_logic` | as `gleann_search` | | | `mcp/server.go:1226-1230` |
| | (dropped) | `graph_context` | | | | hole 7 |
| MCP `gleann_batch_ask` | TopK | `top_k` | NZ (above 0) | ignore | const 5 | `batch_ask.go:114-117` |
| MCP prompts (fixed) | TopK, GraphContext | none | | | const 10, const true | `prompt_tools.go:69, 120` |
| CLI search | TopK | `--top-k` (through the config today) | P | unparsable keeps the previous value | saved `top_k`, then 10 | `config.go:99-103, 358-361` |
| | HybridAlpha | `--no-hybrid` (const 1.0, also `SetScorer(nil)`), then `--hybrid-alpha`, then `--hybrid` (through the config) | P | `--hybrid-alpha`: unparsable ignored, out of range passed; `--hybrid`: unparsable gives 0 (hole 19) | config | `cmd_search.go:75-77, 102-108`, `config.go:137-143` |
| | Rerank | `--rerank`, `--no-rerank` (negated) | P | – | saved `rerank_enabled` | `cmd_search.go:298-303` |
| | RerankPool | `--rerank-pool` | NZ (above 0) | ignore | config | `:109-113` |
| | GraphContext, OnePerFile, IncludeTests | `--graph`, `--one-per-file`, `--include-tests` | P (flag) | – | config | `:99, 114, 117` |
| | Kind | `--kind` | NZ | pass | config | `:120` |
| | Expand | `--expand`, `--no-expand` (negated) | P | – | saved `expand` | `expand_flags.go:9-14` |
| | ExpandModel | `--expand-model` | NZ | – | saved `expand_model` | `:18-27` |
| | (dropped) | `--top`, `--base-url`, `--api-key` | | | | `flagspec.go:53-59` |
| CLI ask | Rerank, GraphContext, Expand | `--rerank`/`--no-rerank`, `--graph`, `--expand`/`--no-expand` (written into the config before the searcher is built) | P | – | saved settings | `cmd_query.go:795-805` |
| | (dropped) | `--rerank-pool` | | | | hole 1 |
| TUI chat | TopK | session value | – | – | saved `top_k`, then 10 (overrides `--top-k`, hole 12) | `tui/chat.go:272-293, 2129` |
| | Rerank | session toggle | – | – | saved `rerank_enabled` | `:276, 1876-1886, 2130`, `agentchat.go:314` |
| A2A search | all | none | | | the searcher's config; no reranker or expander instance exists on this path | `a2a_handler.go:88` |

Not re-read: CLI `--rerank-model`, `--rerank-provider`, `--rerank-url` (`rerankerConfigFromFlags`), the CLI ask `--top-k` and `--hybrid` path after `createAskSearcher`, REST ask beyond `server.go:1004-1010`, the TUI's selected rerank model.

## Appendix B. Config fields per command today

Read for the audit; step 0d records the actual values and corrects this table where it is wrong. "Reference" is `getConfig` + `applySavedConfig`, used by search, ask, def, benchmark and serve.

| Field | Reference | `gleann chat` | TUI (`tui.loadConfig`) | MCP (`runMCPServer` into `mcp.NewServer`) |
|---|---|---|---|---|
| IndexDir | `--index-dir`, then `GLEANN_INDEX_DIR`, then saved `index_dir`, then `~/.gleann/indexes` (`config.go:67-70, 94-98, 348-350`) | flag, then `GLEANN_INDEX_DIR`, then the default; the saved `index_dir` is not applied (`cmd_query.go:558-585`) | saved, then the default (`tui.go:332, 354`) | saved, then `~/.gleann/indexes`; `GLEANN_INDEX_DIR` not read (`cmd_mcp.go:72-73, 100-102`); a roots change overwrites it (`roots.go:46`) |
| OllamaHost | `--host`, then saved with the scheme added, then `OLLAMA_HOST` with the scheme added (`config.go:297-338`) | `--host`, then the saved string as it is (`cmd_query.go:573-575`) | the saved string as it is (`tui.go:342`) | the saved string as it is, else the default (`cmd_mcp.go:64, 82-84`) |
| UseMmap | `--no-mmap`, `GLEANN_MMAP` (`config.go:42-60`) | as reference (through `getConfig`) | not read | not read |
| SearchConfig.TopK | `--top-k`, then saved `top_k`, then 10 (`config.go:99-103, 358-361`) | `--top-k` into the config; the TUI then uses the saved value or 10 (hole 12) | saved or 10 (`chat.go:272-293`) | 10 from `DefaultConfig`; the saved value is not read (hole 6) |
| UseReranker, RerankerConfig, Expand, ExpandModel | saved (`applySavedReranker`, `config.go:425-439`) | not applied here; the TUI reads the saved rerank setting itself | rerank: saved; expand: not applied (hole 12) | not applied; `GLEANN_RERANK*` when a searcher loads (`mcp/server.go:484-493`) |
| SearcherCacheBudget | `GLEANN_SEARCHER_CACHE_BUDGET` (`config.go:61-65`); REST reads the variable again at every eviction (`searcher_cache.go:198-207`) | as reference | not read | `mcp.Config.SearcherCacheBudget` when above 0 (`mcp/server.go:159-161`) |
| LLM provider, model | flags, then saved (`config.go:352-357`) | not re-read | not re-read | saved, then `GLEANN_LLM_PROVIDER`, `GLEANN_LLM_MODEL` (`cmd_mcp.go:94-110`) |
| Backend, StorageMode, code and doc embedding models | reference path only, per the first draft; not re-read | | | |

## Appendix C. Error rendering today

What was read. Step 0b pins these as error-path goldens.

| Situation | REST | MCP | CLI | Proxy | A2A |
|---|---|---|---|---|---|
| invalid index name | 400 "invalid index name: ..." (`server.go:779-781`) | "Error loading index %q: ..." with the validation text (`mcp/server.go:410-412, 1967`) | not re-read | not re-read | "access denied or index %q not accessible" (`isIndexAccessible` returns false, `server.go:1639-1641`) |
| access refused | 403 (`server.go:784-787`; `multi_search_handler.go:126, 168`) | "Error loading index %q: access denied: ..." (`mcp/server.go:418, 429, 1967`), or the busy answer when the name contains one of the substrings below; `gleann_batch_ask` wraps it as "index %q not found: ..." (`batch_ask.go:128-131`) | none on single-index entries; several: dropped silently | alone: error; among several: left out and logged (`proxy_handler.go:559-567`) | error or left out (`a2a_handler.go:60-79`) |
| old store format | 409 (`searcher_cache.go:320-323`) | "Error loading index %q: ..." (`mcp/server.go:1967`), unless the text matches a busy substring | "error loading index: ..." and exit status 1 (`cmd_search.go:180-182`, `cmd_query.go:809-812`) | skip reason "could not be loaded" among several (`proxy_handler.go:520-528`) | skipped silently |
| load invalidated three times | 503 with `Retry-After: 1` (`:324-328`) | not possible today | not possible | skip reason "busy; retry" | skipped silently |
| any other load error | 404 "index %q not found: ..." (`:330`) | "Index %q is currently being built or synced in the background ..." when a sync runs or the text contains `timeout`, `lock`, `bbolt`, `resource temporarily unavailable` or `status 1` (`mcp/server.go:1942-1966`); otherwise "Error loading index %q: ..." (`:1967`) | "error loading index: ..." or "error loading indexes: ..." and exit status 1 | alone: "load index %q: ..."; among several: skipped and reported | skipped silently |
| search error | 500 "search failed: ..." (`server.go:865-869`) | "search error: ..." (`search_progressive.go:131-134`) | "error in multi-search: ..." and exit status 1 (`cmd_search.go:155-158`) | skipped and reported among several | skipped silently |
