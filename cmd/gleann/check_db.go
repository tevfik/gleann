package main

import (
"fmt"
"log"

"github.com/tevfik/gleann/internal/graph/kuzu"
)

func main() {
db, err := kuzu.NewDB("/home/tevfik/.gleann/indexes/test-cpp/kuzu")
if err != nil {
log.Fatal(err)
}
defer db.Close()

syms, err := db.SymbolsInFile("main.cpp")
if err != nil {
log.Fatal(err)
}

fmt.Printf("Symbols found: %d\n", len(syms))
for _, s := range syms {
fmt.Printf("- [%s] %s | %s\n", s.Kind, s.Name, s.FQN)
}
}
