//go:build ignore

package main

import (
"github.com/tevfik/gleann/modules/chunking"
)

func main() {
srcPy := `
def my_func():
    print("hello")
    obj.do_something(1, 2)
`
chunking.DebugTreeSitterCalls(srcPy, chunking.LangPython)

srcGo := `
func main() {
    fmt.Println("hello")
    myFunc()
}
`
chunking.DebugTreeSitterCalls(srcGo, chunking.LangGo)
}
