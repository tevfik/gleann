//go:build treesitter

package indexer

import (
	"fmt"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
)

func main() {
	db, err := kgraph.Open("")
	if err != nil {
		fmt.Printf("open kuzu: %v\n", err)
	}
	defer db.Close()

	idx := New(db, "myproject", "/fake/root")

	src := `
#include <iostream>
#include <string>

std::string format_name(const std::string& name) {
    return "Hello, " + name;
}

void greet(const std::string& name) {
    std::string msg = format_name(name);
    std::cout << msg << std::endl;
}

class MyClass {
public:
    void do_work() {
        greet("world");
    }
};
`
	if err := idx.IndexFile("/fake/root/src/main.cpp", src); err != nil {
		fmt.Printf("IndexFile: %v\n", err)
	}

	symbols, err := db.SymbolsInFile("src/main.cpp")
	if err != nil {
		fmt.Printf("SymbolsInFile: %v\n", err)
	}

	for _, s := range symbols {
		fmt.Printf("SYM [%s] %s | %s\n", s.Kind, s.Name, s.FQN)
	}
}
