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

## Aşama 4: Modüler Refactor (Go Workspace - `go.work`)
- [ ] Projeyi `WORKSPACE` altında bir `go.work` alanına (Multi-Module Workspace) dönüştür.
- [ ] Aşağıdaki gibi bağımsız bileşenleri ayırarak kendi `go.mod` dosyalarını oluştur:
    - `gleann-hnsw` (veya benchmark sonucuna göre vektor DB modülü)
    - `gleann-chunking` (Doküman ve AST metin parçalama)
    - `gleann-core` (Ana CLI, uygulama mantığı)
- [ ] İç içe (circular) bağımlılıkları temizle ve modüller arası interface'leri netleştir.

---
*Not: Bu belge (AGENT_TASKS.md), görevler tamamlandıkça Agent'lar tarafından `[x]` şeklinde güncellenmelidir.*
