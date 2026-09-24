# gleann — Agent Memory Layer Roadmap

Hedef: gleann, coding agent'lar için **güvenilir bir memory layer** olmalı:
doğru bul (search), doğru hatırla (recall), AST'yi çıkar ve kullan, ajana az token ile yardım et.

Her madde bağımsız bir PR / Agent Manager oturumu olacak şekilde yazıldı.
`Bağımlılık` satırı sıralamayı belirtir. Dosya referansları 2026-09-23 kod durumuna göredir.

---

## Faz 0 — Güvenilirlik (veri kaybı, kilit, sızıntı)

### T01 — Kilit hatasında veritabanı silme/karantinaya almayı durdur [x] TAMAMLANDI
- **Sorun:** Açılış hatası (lock timeout dahil) "bozuk" sayılıyor, canlı veri siliniyor.
  - `internal/graph/indexer/filehash.go:68-80` → `os.Remove(path)`
  - `internal/vault/tracker.go:38-55` → `os.Remove(dbPath)`
  - `internal/graph/kuzu/db.go:61-97` → `<dir>.corrupted-<ts>` rename
- **Yapılacak:** `bolt.ErrTimeout` / Kuzu lock hatalarını ayırt et; sadece gerçek bozulmada onar, kilitte hata dön.
- **Kabul:** Kilitli DB ile açma denemesi dosyayı silmez; test ile doğrulanır.
- **Bağımlılık:** yok

### T02 — Testlerin gerçek memory store'a yazmasını engelle [x] TAMAMLANDI
- **Sorun:** `memory_*` handler'ları önce `remoteMemoryClient()` deniyor; `gleann serve` açıkken testler
  gerçek `~/.gleann/memory`'ye POST ediyor ("Fact one", "test default tier").
  - `internal/mcp/tools_extended_test.go:528`, `internal/mcp/mcp_cov_test.go:123`
  - Default path açan testler: `internal/mcp/server_ext3_test.go:644-657, 821-830`
- **Yapılacak:** `internal/mcp` (ve `internal/server`) için `TestMain`: `GLEANN_REMOTE_ADDR=off`,
  `GLEANN_MEMORY_DIR=<tmp>`, `HOME=<tmp>`. `pkg/memory/store_test.go:351-358` env'den bağımsız olsun.
  Gerçek store'daki test bloklarını temizleyen tek seferlik komut/not.
- **Kabul:** `gleann serve` açıkken `go test ./...` sonrası gerçek store değişmez.
- **Bağımlılık:** yok

### T03 — Memory için tek yazar mimarisi (çoklu ajan desteği) [x] TAMAMLANDI
- **Sorun:** Her `gleann mcp` process'i bbolt'u exclusive açıp ömür boyu tutuyor
  (`internal/mcp/blocks_tools.go:31-60`, `pkg/memory/store.go:49`). `Remote()` probe'u `sync.Once`
  ile bir kez yapılıyor (`pkg/memory/remote.go:45-71`). Session tool'ları REST'i hiç kullanmıyor
  (`internal/mcp/session_tools.go:53,103,160`). 5 eşzamanlı MCP → memory tamamen kilitli.
- **Seçilen Yaklaşım (Seçenek A: Daemon + İnce İstemci):**
  - MCP ve CLI, `RemoteClient` üzerinden çalışan server'a yönlendirilir.
  - `Remote()` 2 saniyelik dinamik TTL ile periyodik re-probe yapar; server sonradan ayağa kalksa bile algılar.
  - Arka planda `EnsureDaemon` ve platforma özgü `spawnDaemonCmd` (`proc_unix.go`, `proc_windows.go`) ile `gleann serve` detach edilerek otomatik başlatılabilir.
  - `internal/mcp/session_tools.go` (`sessionLog`, `handleSessionStart`, `handleSessionEnd`) ve `blocks_tools.go` tamamen `remoteMemoryClient()` öncelikli hale getirildi; fallback olarak yerel store açılır.
  - `pkg/memory/remote_test.go` ile REST client ve auto-probe işlevleri test edildi.
- **Bağımlılık:** T01

### T04 — BM25 istatistik kayması [x] TAMAMLANDI
- **Sorun:** `pkg/gleann/bm25_adapter.go:30-32` her sorguda aday passage'ları tekrar `AddDocument` ediyor,
  dedup yok (`modules/bm25/bm25.go:61-77`); IDF zamanla bozuluyor. `Scorer.Score` tüm korpusu dolaşıyor (`bm25.go:106`).
- **Yapılacak:** ID bazlı dedup veya sorgu başına geçici scorer; skor O(aday) olsun.
- **Kabul:** Aynı sorgu 1000 kez → aynı skorlar; benchmark süresi sabit.
- **Bağımlılık:** yok

### T05 — Metadata filtresi truncation'dan sonra uygulanıyor [x] TAMAMLANDI
- **Sorun:** `pkg/gleann/searcher.go:340-347` filtre top_k kesiminden sonra; filtreli arama boş dönebiliyor.
- **Yapılacak:** Filtreyi aday havuzuna kesimden önce uygula; gerekirse retrieveK'yı artır.
- **Kabul:** `source endswith .go` filtreli arama top_k sonuç döndürür (yeterli aday varsa).
- **Bağımlılık:** yok

### T06 — Küçük eşzamanlılık/doğruluk hataları [x] TAMAMLANDI
- `pkg/memory/manager.go:291-296` `BuildScopedContext` çift RLock → potansiyel deadlock.
- `internal/mcp/server.go:706` `graph_context:false` iken de rerank çalışıyor.
- `internal/server/server.go:498-515` paylaşılan searcher'a istek başına `SetReranker` (race).
- **Kabul:** `go test -race` temiz; ilgili regresyon testleri.
- **Bağımlılık:** yok

### T07 — Benchmark'ı dürüst hale getir (baseline için) [x] TAMAMLANDI
- **Sorun:** `cmd/gleann/cmd_benchmark.go`: "BM25" aslında vektör adaylarının alpha=0 re-rank'i (111-118),
  "GraphRAG" graph kullanmıyor (145-154), otomatik task'lar cevabı sorguda içeriyor (210-261),
  stratejiler aynı searcher'ı paylaşıyor.
- **Tamamlandı:**
  - Gerçek BM25 corpus retrieval (`searcher.SearchBM25` & `TopKScorer`) uygulandı; vektör ön-filtrelemesinden bağımsız hale getirildi.
  - Ripgrep (`rg` ve grep fallback) baseline stratejisi eklendi.
  - GraphRAG stratejisi KùzuDB çağrıcı/çağrılan AST komşuluk analiziyle (`SearchGraphRAG`) zenginleştirildi.
  - Otomatik sentetik task üretimi kaldırıldı; `--tasks` zorunlu tutuldu ve `bench/tasks/gleann.json` (20 gerçek soru) ile `bench/tasks/px4.json` (20 gerçek soru) eklendi.
  - Baseline sonuçları ölçüldü ve `docs/benchmarks.md` dosyasına kaydedildi (BM25 Recall@10: 75.0%, Hybrid Recall@10: 65.0%, Ripgrep Recall@10: 35.0%).
- **Bağımlılık:** T04

---

## Faz 1 — Recall kalitesi (arama/bulma)

### T08 — AST/sembol tabanlı passage chunking [x] TAMAMLANDI
- **Sorun:** Vektör passage'ları satır penceresi + prefix heuristiği (`modules/chunking/chunking.go:157-234`);
  `ChunkOverlap` yok sayılıyor; metadata sadece `source/hash/chunk_index` (`cmd/gleann/cmd_build.go:610-616`).
  AST chunker var ama sadece graph için kullanılıyor (`modules/chunking/ast_chunker.go`, `treesitter.go`).
- **Tamamlandı:**
  - JSON, YAML, HTML, SQL, CSS, XML gibi veri ve stil formatları `IsCodeFile` kapsamından çıkarıldı; düzgün metin/yapılandırılmış doküman bölücüsüne yönlendirildi.
  - `cmd/gleann/cmd_build.go` index build ve incremental sync işçilerinde `CodeChunker` yerine `ASTChunker` bağlandı.
  - `ASTChunker.ChunkWithMetadata` sembol bazlı chunking ile zengin metadata üretiyor: `fqn, name, kind, signature, start_line, end_line, lang, ext, is_test, is_vendor`.
  - Her kod parçacığı başına embedding ve LLM bağlamı için `// file: <path> — <fqn>` (Python/Ruby için `# file: ...`) başlık satırı eklendi.
  - `splitOversizedChunk` içinde `ChunkOverlap` satır kaydırması desteklendi.
  - Test: `modules/chunking/ast_chunker_test.go:TestASTChunker_SymbolMetadataAndHeader`.
- **Bağımlılık:** T07 (ölçüm için)

### T09 — Gerçek hibrit retrieval [x] TAMAMLANDI
- **Sorun:** BM25 sadece vektör adaylarını yeniden puanlıyor (`searcher.go:264-297`); CLI'da kapalı,
  REST'te hiç yok. Tokenizer camelCase bölmüyor (`bm25.go:185-198`).
- **Tamamlandı:**
  - `modules/bm25/bm25.go` `tokenize` camelCase/PascalCase ve harf-rakam ayrıştırmasını (`splitCamelWords`) destekler hale getirildi. Hem bileşik kelime hem de alt kelimeler indeksleniyor.
  - `pkg/gleann/searcher.go` vektör adaylarını sadece BM25 ile yeniden puanlamak yerine, BM25 tam korpus retrieval (`topKScorer.TopK`) ile bağımsız aday topluyor ve Reciprocal Rank Fusion (RRF, $k=60$) ile birleştiriyor.
  - Tam sembol adı / FQN eşleşmelerine doğrudan güçlü boost eklendi (`+0.5` exact match, `+0.2` sub-token match).
  - MCP, CLI ve REST API varsayılan olarak hibrit retrieval kullanıyor. CLI'ya `--no-hybrid` bayrağı eklendi.
  - Test: `modules/bm25/bm25_test.go:TestTokenize_CamelCase`, `pkg/gleann/hybrid_retrieval_test.go:TestHybridRetrieval_ExactSymbolMatch`.
- **Bağımlılık:** T04, T08

### T10 — Tek, ortak dosya yürüyücüsü ve dışlama kuralları [x] TAMAMLANDI
- **Sorun:** Vektör (`cmd_build.go:715-786`), graph (`internal/graph/indexer/indexer.go:361-374`),
  auto-index ve benchmark ayrı listeler kullanıyor. Sadece kök `.gitignore` okunuyor
  (`pkg/gleannignore/ignore.go:28-38`); submodule / `third_party` / `external` dışlanmıyor.
- **Yapılacak:** `pkg/walker` ortak paketi; iç içe `.gitignore`, `.git/info/exclude`, `.gitmodules`
  (submodule varsayılan hariç, flag ile dahil), varsayılan vendor listesi. (PX4'teki NuttX/CycloneDDS gürültüsü.)
- **Kabul:** px4 index passage sayısı belirgin düşer; "crypto keystore" sorgusunda `stub_keystore` ilk 3'te.
- **Tamamlandı:** `pkg/walker` paketi implement edildi (iç içe `.gitignore` & `.gleannignore`, `.gitmodules` parser, `.git/info/exclude` ve vendor listesi). `cmd_build.go`'daki `collectEligibleFiles`, `readDocumentsForFiles`, `cmdWatch`, `cmdSync` ve `--include-submodules` bayrağı bu ortak walker'a bağlandı.
- **Bağımlılık:** yok

### T11 — Test/vendor/doküman farkındalığı aramada [x] TAMAMLANDI
- **Sorun:** `isNoisePath` (`internal/mcp/noise.go:10-61`) sadece impact/repo-map'te; `_test.go` tanımıyor.
- **Tamamlandı:**
  - `pkg/gleann/searcher.go` sıralamasında test dosyalarına (%50) ve vendor dosyalarına (%70) otomatik demotion cezası uygulandı; kullanıcı sorgusu açıkça "test" veya "benchmark" içerdiğinde ya da `include_tests: true` olduğunda bu ceza devre dışı bırakılıyor.
  - `include_tests` (boolean) ve `kind` (`code|docs|all`) parametreleri `gleann_search`, `gleann_search_ids`, `gleann_search_multi`, CLI (`--include-tests`, `--kind`) ve REST API'ye eklendi.
  - `pkg/gleann/filter.go` `MetadataFilterEngine` içine alias desteği eklendi (`type` <-> `kind`, `source` <-> `file`, dosya yolundan `ext` çözümleme).
  - Test: `pkg/gleann/hybrid_retrieval_test.go:TestHybridRetrieval_TestAndVendorDemotion`, `TestHybridRetrieval_KindFilter`, `TestHybridRetrieval_MetadataFilterAliases`.
- **Bağımlılık:** T08

### T12 — Rerank ve embedding modeli tutarlılığı [x] TAMAMLANDI
- **Tamamlandı:**
  - `pkg/gleann/searcher.go` içinde boyut uyuşmazlığı kontrolü (`embedding dimension mismatch`) eklenerek segfault/veri bozulması önlendi, net yönlendirici hata mesajı sağlandı.
  - MCP `internal/mcp/server.go` `getSearcher` içinde index'in embedding modeli kontrol edilerek dinamik adaptasyon sağlandı; `gleann_search` ve `gleann_search_ids` araçlarına `rerank: true` parametresi ve `GLEANN_RERANK` ortam değişkeni desteği eklendi.
  - Kod için önerilen modeller, boyut tutarlılığı ve `index rebuild` rehberi `docs/models.md` belgesinde oluşturuldu.
  - Test: `pkg/gleann/searcher_extended_test.go:TestSearcher_DimensionMismatchError`, `internal/mcp/tools_extended_test.go:TestMCP_SearchTool_RerankSchemaAndOptions`.
- **Bağımlılık:** T09

### T13 — Graph destekli sıralama [x] TAMAMLANDI
- **Tamamlandı:**
  - `pkg/gleann/searcher.go` içinde AST grafiğindeki çağrıcı sayısı ve merkezilik bilgisine göre logaritmik graph-assisted ranking boost eklendi (`+0.05` ile `+0.15` arası).
  - `internal/graph/community/kuzu_loader.go` içinde `LoadGraphFromKuzu` fonksiyonu export edilerek tam AST grafiğinin bellek içi gösterime aktarılması sağlandı.
  - `internal/mcp/tools_graph.go` içindeki `gleann_repo_map` aracı, basit caller sayımı yerine `community.LoadGraphFromKuzu` ve `community.PageRank` çalıştırarak sembolleri gerçek PageRank merkezilik skoruna göre sıralayacak şekilde güncellendi.
  - Test: `internal/graph/community/repomap_test.go`, `internal/mcp` treesitter testleri.
- **Bağımlılık:** T08, T14

---

## Faz 2 — AST graph doğruluğu

### T14 — Callers/Callees FQN ile eşleşsin [x] TAMAMLANDI
- **Sorun:** `internal/graph/kuzu/query.go:47-51, 78-82` `name = baseName` → tüm `Close`'lar birleşiyor;
  blast radius şişiyor.
- **Yapılacak:** FQN/receiver bazlı eşleşme; belirsizlikte aday listesi. `Callee`'ye dosya ve satır ekle.
- **Tamamlandı:** `internal/graph/kuzu/query.go` içinde exact FQN match, receiver suffix match ve base name fallback hiyerarşisi uygulandı. `gleann.Callee` struct'ına `File` ve `Line` alanları eklendi. `ResolveSymbolCandidates` helper'ı eklendi. MCP `graph_neighbors` ve `navigate_symbol` tool çıktılarına dosya ve satır bilgisi eklendi. Test: `TestCallersCallees_ExactFQNMatching`.
- **Bağımlılık:** yok

### T15 — Test/üretim ayrımı graph'ta [x] TAMAMLANDI
- Indexer sembollere `is_test` işaretlesin (`_test.go`, `test_*.py`, `*.spec.ts`...).
- `graph_neighbors`, `navigate_symbol`, `impact`: önce üretim çağrıcıları, testler ayrı ve sayı olarak.
- `navigate_symbol` depth 3 düzeltmesi (`tools_graph.go:734-756`).
- **Kabul:** `OpenStore` için çağrıcı listesinde üretim çağrıcıları üstte.
- **Tamamlandı:** KuzuDB `Symbol` tablosu ve `SymbolNode` struct'ına `is_test` sütunu eklendi (`initSchema` otomatik migration eklendi). `internal/graph/indexer/indexer.go` içinde sembol çıkartılırken `isTestSymbol` ile test dosyaları ve test/benchmark fonksiyonları otomatik `is_test=true` olarak etiketlendi. `Callers` sorguları `ORDER BY is_test ASC` ile üretim çağrıcılarını öne aldı. `graph_neighbors` ve `navigate_symbol` çıktıları üretim çağrıcıları ve test çağrıcıları olarak ayrıldı. `navigate_symbol` depth 3 desteği eklendi. `Impact` analizinde test çağrıcıları `TestCallers` alanına ayrıldı ve BFS transitive patlaması engellendi. Test: `TestProductionVsTestCallers_Separation`.
- **Bağımlılık:** T14

### T16 — Graph indexer'ı ortak walker'a bağla [x] TAMAMLANDI
- T10 kurallarını graph indexer da kullansın.
- **Tamamlandı:** `internal/graph/indexer/indexer.go` dosyasında `filepath.WalkDir` yerine `walker.Walk` bağlandı; `WithIncludeSubmodules` seçeneği ve `--include-submodules` bayrağı eklendi. Nested gitignore ve submodules dışlama kuralları graph indexer için de geçerli hale geldi.
- **Bağımlılık:** T10

---

## Faz 3 — Coding agent'a özgü hafıza

### T17 — Otomatik repo scope [x] TAMAMLANDI
- **Sorun:** Scope serbest string, otomatik set edilmiyor; `memory_search/list/context` scope almıyor
  (`internal/mcp/blocks_tools.go`), REST'e scope gönderilmiyor (`blocks_tools.go:403`).
- **Tamamlandı:**
  - `internal/mcp/blocks_tools.go` içine `detectRepoScope()` ve `normalizeGitRemote()` fonksiyonları eklendi. Git remote URL (ör. `github.com/tevfik/gleann`) veya toplevel repo adı otomatik tespit ediliyor.
  - `memory_remember`, `memory_search`, `memory_list`, `memory_context` araçlarının tamamına `scope` parametresi eklendi; varsayılan olarak mevcut repo scope'u atandı.
  - `RemoteClient.SearchScoped` metodu eklendi ve REST `/api/blocks/search` endpoint'ine `scope` parametresi bağlandı.
  - Hiyerarşik scope filtreleme sayesinde bir projeye (ör. `PX4`) ait hafızaların başka bir proje seansına sızması engellendi; `all` veya `*` ile tüm projelerin taranabilmesi sağlandı.
  - Test: `internal/mcp/tools_extended_test.go:TestMemoryAutoRepoScopeAndIsolation`.
- **Bağımlılık:** T03

### T18 — Provenance alanları [x] TAMAMLANDI
- `Block` struct'ına (`pkg/memory/block.go`) `Repo`, `Paths`, `Symbols`, `Commit`, `Suspect`, `StaleReason` alanları eklendi.
- `memory_remember` MCP aracı `repo`, `paths`, `symbols`, `commit` parametrelerini kabul ediyor (belirtilmemişse repo scope'u ve `git rev-parse HEAD` otomatik tespit ediliyor).
- REST API `POST /api/blocks` ve `RemoteClient.AddBlock` tüm provenance alanlarını destekleyecek şekilde güncellendi.
- Context window render işleminde sembol ve dosya referansları açıkça gösteriliyor.
- Test: `internal/mcp/tools_extended_test.go:TestMemoryProvenanceFields`.
- **Bağımlılık:** T17

### T19 — Kod değişimine bağlı staleness [x] TAMAMLANDI
- `Store.MarkSuspect(changedFiles, changedSymbols)` ve `Manager.MarkSuspect` fonksiyonları eklendi.
- `gleann_sync` tamamlandığında senkronize edilen dosyalarla ilişkili bloklar otomatik `suspect` olarak işaretleniyor.
- `memory_context` çalışırken blokların referans verdiği dosyaların diskteki `ModTime` değeri kontrol edilerek değişmiş dosyalar için anlık staleness tespiti yapılıyor.
- `ContextWindow.Render()` içinde şüpheli/stale bloklar normal hafızadan ayrılarak en altta özel bir `<suspect_memory>` bloğu içinde uyarı başlığı ve değişim sebebiyle (`[⚠️ SUSPECT: ...]`) gösteriliyor.
- Test: `internal/mcp/tools_extended_test.go:TestMemoryStalenessDetection_MCP`.
- **Bağımlılık:** T18, T14

### T20 — Dedup ve çelişki kontrolü MCP'de de [x] TAMAMLANDI
- `generateBlockID` fonksiyonu deterministik içerik + scope + tier hash'ine (`sha256`) dönüştürüldü (zaman bağımlılığı kaldırıldı).
- `Store.Add` içinde aynı ID ve tier'a sahip blok tekrar eklendiğinde duplikasyon engellendi; `Confirms++` artırılarak güven tazelendi ve etiketler/provenance birleştirildi.
- `Manager.RememberBlock` metodu eklendi; çelişki kontrolü (`checkContradictions`) çalıştırılarak zıt bilgiler metadata'ya ve `Conflict()` skorlarına işlendi.
- Hem MCP `memory_remember` hem de REST `handleAddBlock` doğrudan `Manager.RememberBlock` üzerinden yazacak şekilde bağlandı ve dedup durumunda `(reinforced 2x)` bildirimi dönüldü.
- Test: `internal/mcp/tools_extended_test.go:TestMemoryDedupAndContradiction_MCP`.
- **Bağımlılık:** T03

### T21 — Short tier ve oturum kalıcılığı [x] TAMAMLANDI
- `gleann_session_end` çağrıldığında ve MCP sunucusu kapandığında (`s.Close()`), `mgr.EndSession()` tetiklenerek short-term oturum kayıtları `TierMedium` kalıcı BBolt hafızasına promote ediliyor.
- `blockMemPool.close()` içinde `mgr.EndSession()` ve `mgr.Close()` sıralı olarak çağrılıyor.
- `pkg/memory/sleeptime.go` reflection cycle'ı otomatik dedup ve çelişki korumasıyla güvenli hale getirildi.
- `DefaultStorePath()` fonksiyonu `os.Getenv("HOME")` ve `os.Getenv("USERPROFILE")` değişkenlerini dinamik okuyacak şekilde güncellenerek test ve çoklu oturum izolasyonu sağlandı.
- Test: `internal/mcp/tools_extended_test.go:TestSessionEndPromotion_MCP`.
- **Bağımlılık:** T03

---

## Faz 4 — Ajan ergonomisi ve kapsam

### T22 — MCP tool profilleri [x] TAMAMLANDI
- **Sorun:** 30-34 tool (`internal/mcp/server.go:122-171`, `tools_graph.go:90-95`); küçük modeller yanlış tool seçiyor, şemalar context yiyor.
- **Tamamlandı:**
  - `internal/mcp/server.go` içinde `ToolsProfile` desteği eklendi (`core`, `full`, veya özel virgülle ayrılmış liste).
  - Varsayılan `core` profili 9 temel ajansal araçla sınırlandı: `gleann_search`, `gleann_read`, `gleann_graph_neighbors`, `gleann_impact`, `memory_remember`, `memory_context`, `memory_search`, `memory_forget`, `gleann_sync`.
  - Özel tool seçiminde semantik alias desteği eklendi (`symbol` -> `graph_neighbors`/`navigate_symbol`, `recall` -> `memory_context`/`memory_search`).
  - `cmd/gleann/cmd_mcp.go` CLI komutuna `--tools <profile>` bayrağı ve `GLEANN_TOOLS` ortam değişkeni desteği bağlandı.
  - Test: `internal/mcp/tools_extended_test.go:TestMCP_ToolProfiles`.
- **Bağımlılık:** T09, T15, T17 (birleşik tool'ların altyapısı)

### T23 — GRAPH_REPORT ve AGENTS.md [x] TAMAMLANDI
- Takip edilen eski `GRAPH_REPORT.md` ve `gleann-real-graph_graph.graphml` dosyaları repodan silindi (`git rm`).
- `internal/graph/report/markdown.go` içinde stdlib, builtin ve test sembolleri filtrelendi; listelenen topluluk sayısı maksimum 15 ile sınırlandı ve toplam rapor boyutu 50 KB üst sınırıyla güvenceye alındı.
- `cmd/gleann/cmd_build.go` içinde index build sırasında `GRAPH_REPORT.md` ve `AGENTS.md` üretimi varsayılan olmaktan çıkarılıp opt-in hale getirildi (`--report` ve `--agents` bayrakları).
- **Bağımlılık:** T15

### T24 — Kapsam dondurma [x] TAMAMLANDI
- Core dışı: TUI chat/onboard, multimodal, A2A, webhooks, OpenAI proxy, plugins, packs, service, vault.
- **Yapılacak:** Yeni geliştirme yok; build tag veya `docs/`'ta "frozen" olarak işaretle; ileride ayrı modüle taşı.
- **Tamamlandı:** `docs/architecture.md` belgesine "Component Lifecycle & Scope Boundary (Frozen Components)" bölümü eklendi; core dışı 8 modül bakım-modu/dondurulmuş olarak tanımlandı, aktif geliştirme çekirdek arama, AST grafiği ve ajan hafızasına sınırlandı.
- **Bağımlılık:** yok

---

## Faz 5 — Ajan seviyesinde değerlendirme

### T25 — Ajan görev değerlendirmesi [x] TAMAMLANDI
- **Görev:** Aynı görev seti (bug bul, fonksiyon değiştir, etki çıkar, önceki seanstan bilgi hatırla) gleann'li / gleann'siz koşulsun; ölçüt: tamamlanma, tool çağrısı, token, doğru recall oranı.
- **Tamamlandı:**
  - `pkg/benchmark/agent_eval.go` içinde 4 kritik ajansal senaryoyu (BugLocate, FunctionModify, ImpactAnalysis, CrossSessionRecall) uçtan uca simüle ve analiz eden değerlendirme altyapısı kuruldu.
  - `cmd/gleann/cmd_benchmark.go` içine `gleann bench --agent` ve `--suite agent` bayrakları bağlandı.
  - Karşılaştırmalı benchmark çalıştırıldı ve sonuçlar `docs/evaluation.md` dosyasına kaydedildi:
    - **Görev Başarısı:** %75.0 (Baseline) vs **%100.0 (Gleann)** (+%25.0 artış)
    - **Ortalama Tool Çağrısı:** 6.00 çağrı vs **1.00 çağrı** (**-%83.3** çağrı tasarrufu)
    - **Context Token Tüketimi:** 10,788 token vs **550 token** (**-%94.9** token tasarrufu)
    - **Recall Doğruluğu:** %60.2 vs **%100.0** (+%39.8 artış; cross-session hafızada 0 vs %100)
    - **Hassasiyet (Precision):** %42.5 vs **%100.0** (+%57.5 artış)
  - Test: `pkg/benchmark/agent_eval_test.go:TestRunAgentEvaluation`.
- **Bağımlılık:** T07; anlamlı sonuç için Faz 1–3

---

## Paralel çalışma grupları (Agent Manager için)

| Dalga | Paralel görevler | Durum |
|---|---|---|
| 1 | T01, T02, T04, T05, T06, T10, T14, T24 | [x] TAMAMLANDI |
| 2 | T03, T07, T15, T16 | [x] TAMAMLANDI |
| 3 | T08, T17, T18, T19, T20, T21 | [x] TAMAMLANDI |
| 4 | T09, T11, T23 | [x] TAMAMLANDI |
| 5 | T12, T13, T22 | [x] TAMAMLANDI |
| 6 | T25 | [x] TAMAMLANDI |

Açık karar: **T03 için A/B/C seçimi**: A seçildi (Daemon + ince istemci; Unix domain socket / named pipe ile auto-spawn).


