//go:build cgo && treesitter
package chunking

import (
"context"
"fmt"
sitter "github.com/smacker/go-tree-sitter"
)

func DebugTreeSitterCalls(source string, lang Language) {
parser := getParser(lang)
defer returnParser(lang, parser)
tree, _ := parser.ParseCtx(context.Background(), nil, []byte(source))
root := tree.RootNode()

var walk func(n *sitter.Node)
walk = func(n *sitter.Node) {
if n.Type() == "call_expression" || n.Type() == "call" {
fmt.Printf("found call node: %s -> %q\n", n.Type(), n.Content([]byte(source)))
// child by field 'function'
funcNode := n.ChildByFieldName("function")
if funcNode != nil {
fmt.Printf("   func name: %q\n", funcNode.Content([]byte(source)))
}
}
for i := 0; i < int(n.ChildCount()); i++ {
walk(n.Child(i))
}
}
walk(root)
}
