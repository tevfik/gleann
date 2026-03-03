# Gleann - Embedded Architecture & Modularity Roadmap

Bu belge, Gleann projesini tam bağımsız (embedded) ve modüler bir mimariye geçirmek için tasarlanmıştır. Diğer AI Agent'lar tarafından durum takibi ve görev dağılımı için bir rehber niteliğindedir.

**Güncel Durum / Kararlar:**
*   **FAISS vs HNSW:** FAISS ve HNSW arasında performans kıyaslaması (benchmark) yapılana kadar Linux tarafında **FAISS projede tutulacaktır.**
*   **Graph DB:** AST analizi için KuzuDB (CGO tabanlı) kullanılacaktır.
*   **Local AI:** llama.cpp (önceden derlenmiş binary'lerin `//go:embed` ile gömülmesi) stratejisi uygulanacaktır.
*   **Modülerlik:** Sub-component'ler ayrı Go modülleri (`go.work`) olarak ayrılacaktır.

---

## Aşama 1: Mevcut Durumun Korunması ve Benchmarking
- [x] Şu anki değişiklikleri commit et (Git status temizlendi).
- [x] **FAISS ve HNSW Performans Kıyaslaması (Benchmark):**
    - [x] Linux üzerinde FAISS ve HNSW için Vektör ekleme, arama hızı ve bellek tüketimi (RAM) testlerini içeren bir benchmark veya Python script'i yaz.
    - [x] Sonuçlara göre HNSW'nin FAISS yerine tamamen geçip geçemeyeceğine karar ver (Eğer HNSW yeterliyse FAISS bağımlılığı kaldırılacak, yetersizse CGO/FAISS çözümleri iyileştirilecek).
      - **Sonuç:** FAISS (C++) 10.000 vektör eklemesini ~1.7 saniyede yaparken, saf Go tabanlı HNSW ~26 saniye sürdü. Arama hızlarında FAISS (~0.5ms) HNSW'den (~2.5ms) 5 kat daha hızlı.
      - **Karar:** FAISS'in muazzam performans farkı nedeniyle Linux tarafında projede **tutulmasına** karar verildi. HNSW bir "fallback" (CGO olmayan ortamlar için yedek) olarak kalacaktır.
- [x] **HNSW Goroutine Optimizasyonu İncelemesi:** HNSW ekleme (Insertion) algoritması `graph.mu.Lock()` ile tüm ağacı kilitlediğinden Goroutine kullanımının kilit çekişmesi yaratacağı ve performansı artırmayacağı anlaşıldı (İptal edildi).
- [x] **Weaviate Embedded Alternatifi:** Weaviate Embedded özelliğinin Go resmi istemcisinde desteklenmediği (sadece HTTP/gRPC istemcisi olduğu), yalnızca Python ve Node.js için yerleşik çalışma desteği sunduğu anlaşıldı (İptal edildi).
- [x] **Chromem-go Alternatifi:** Tamamen Go tabanlı, CGO içermeyen bir Vector DB olan `chromem-go` incelendi. 10.000 veri için Insert hızı anlık (~30ms) olsa da, Exhaustive (O(N)) arama yaptığı için sorgu başına arama süresi ~7.3ms sürdü. Gleann'in mevcut HNSW yapısı (sorgu başı 135ns) arama hızında ~54.000 kat daha hızlı olduğu için bu ölçekteki veriler için chromem-go yetersiz bulundu (İptal edildi).

## Aşama 2: llama.cpp (Model Server) Embedding Entegrasyonu
- [ ] **Binary Temini:** Windows (`.exe`), macOS ve Linux için `llama-server` binary'lerini indir/derle. (Linux PoC tamamlandı).
- [ ] **Go Embed Mekanizması:**
    - [x] Bu binary'leri `//go:embed` kullanarak Go uygulamasının içerisine paketle.
    - [x] `runtime.GOOS` kontrolü ile çalışma anında işletim sistemine uygun binary'i geçici (`/tmp` veya `~/.gleann/tmp`) bir dizine çıkar.
- [x] **Soket / Local İletişim:**
    - [x] Çıkarılan `llama-server`'ı dış IP'lere kapalı (`127.0.0.1` veya Unix Socket) bir portta arka plan işlemi (child process) olarak ayağa kaldır.
    - [x] `internal/embedding` ve `pkg/gleann/chat.go` içerisindeki HTTP istemci (client) kodlarını bu yeni lokal adrese istek atacak şekilde güncelle.
- [x] **TUI / Setup Entegrasyonu:**
    - [x] Kullanıcının `gleann setup` üzerinden llama.cpp arka ucunu (backend'ini) seçmesi sağlandı.
    - [x] Model (gguf dosyası) bulunamadığında indirme yönergeleri (path istemi) TUI aracılığıyla kullanıcıya sunuldu.

## Aşama 3: KuzuDB (Graph DB) AST Entegrasyonu
- [x] **KuzuDB Kurulumu:** `github.com/kuzudb/go-kuzu v0.11.3` paketini projeye dahil edildi.
- [x] **Şema (Schema) Tasarımı:** `CodeFile`, `Symbol` node table'ları ve `DECLARES`, `CALLS`, `IMPLEMENTS`, `REFERENCES` ilişki tabloları tasarlandı.
- [x] **Proof of Concept (PoC):** `internal/graph/kuzu` paketi oluşturuldu (`db.go`, `writer.go`, `query.go`). `TestKuzuPoc` testi başarıyla geçti: UpsertFile, UpsertSymbol, AddDeclares, AddCalls, Callees ve SymbolsInFile metodları doğrulandı.
- [ ] KuzuDB'yi `internal/vault` (veya yeni bir modül) içerisine entegre ederek uygulamanın bir parçası yap.

## Aşama 3b: AST Graph Indexer (`internal/graph/indexer`)

> Mevcut `internal/chunking` paketi AST parse ediyor. Bu aşamada parse edilen sembolleri KuzuDB'ye yazan bir köprü katmanı oluşturulacak.

- [x] **`internal/graph/indexer` paketi oluştur:**
    - [x] `indexer.go` → `Indexer` struct, `New(db, module)`, `IndexFile(path, source)`, `IndexDir(root)` fonksiyonları.
    - [x] `go_calls.go` → Go dosyaları için `go/ast` + `ast.Inspect` kullanarak `*ast.CallExpr` çağrı ilişkilerini çıkart. Her çağrı hedefi için `module + pkg + funcName` FQN oluştur ve `AddCalls` ile KuzuDB'ye yaz.
    - [x] `indexer_test.go` → `TestIndexerGoFile` testi geçti: 4 sembol (function/method/struct), Greet→format CALLS ilişkisi doğrulandı.
- [x] **Multidil desteği (tree-sitter):** Python, JS/TS için tree-sitter `call_expression` node tipi ile çağrı ilişkileri.
- [x] **CLI Entegrasyonu:**
    - [x] `gleann build --graph` flag'i ekle: embedding index oluştururken paralelde graph da doldurulsun.
    - [x] `gleann graph deps <symbol>` komutu: bir sembolün bağımlılık ağacını göster.
    - [x] `gleann graph callers <symbol>` komutu: bir fonksiyonu kimin çağırdığını listele.
- [x] **Genişletilmiş Multidil Desteği:**
    - [x] `C, C++, Rust` gibi diller için tree-sitter ile CALLS edge yeteneklerinin `ts_calls.go` içine eklenmesi.
- [x] **Model Yönetimi ve TUI:**
    - [x] `gleann setup` ve `tui` içeriklerinde local LLM indirme mantığının llama.cpp'ye göre düzenlenmesi ve test edilmesi.
- [x] **Build Sistemi Standardizasyonu:**
    - [x] `gleann` ve `gleann-sound` projelerinin Makefile yapıları `build/` dizini tabanlı olacak şekilde senkronize edildi.
    - [x] `go.work` dosyasına plugin projeleri (`gleann-sound`) dahil edilerek workspace bütünlüğü sağlandı.

## Aşama 4: Modüler Refactor (`go.work` ile Tek-ya da Çok-Repo)

> Not: Alt modüller aynı repo içinde `WORKSPACE/gleann-hnsw/` gibi dizinlerde de kalabilir. Ayrı Git repo'su zorunlu değil.

- [x] **`go.work` Kurulumu:**
    - [x] `WORKSPACE/go.work` dosyası oluştur ve mevcut `gleann/` modülünü ekle.
    - [x] Her yeni alt modül bu dosyaya `use` direktifi ile eklenir.
- [x] **`gleann-hnsw` çıkarılması (En kolay — sıfır dış bağımlılık):**
    - [x] `internal/backend/hnsw/` içeriğini `../gleann-hnsw/` dizinine taşı.
    - [x] `module github.com/tevfik/gleann-hnsw` ile yeni `go.mod` oluştur.
    - [x] Ana `gleann/` projesinde import path'leri güncelle.
    - [x] `go.work`'e `use ../gleann-hnsw` ekle.
    - [x] `go test ./...` ile tüm testlerin geçtiğini doğrula.
- [x] **`gleann-bm25` çıkarılması (Kolay — sıfır bağımlılık):**
    - [x] `internal/bm25/` → `../gleann-bm25/` dizinine taşı.
    - [x] `module github.com/tevfik/gleann-bm25` go.mod oluştur.
    - [x] `go.work`'e `use ../gleann-bm25` ekle.
- [x] **`gleann-chunking` çıkarılması (Orta — interface bağımlılığı var):**
    - [x] `gleann.Item` bağımlılığını `chunking.Chunk` lokal interface ile kır.
    - [x] `internal/chunking/` → `../gleann-chunking/` taşı.
    - [x] `module github.com/tevfik/gleann-chunking` go.mod oluştur.

---
*Not: Bu belge (AGENT_TASKS.md), görevler tamamlandıkça Agent'lar tarafından `[x]` şeklinde güncellenmelidir.*
