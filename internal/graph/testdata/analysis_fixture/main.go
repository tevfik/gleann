package main

import (
	"fmt"

	"example.com/fixture/service"
	"example.com/fixture/store"
)

func main() {
	svc := service.New(store.NewMemStore())
	_ = svc.Save("hello", "world")
	fmt.Print(svc.Report())
	run(svc)
}

func run(svc *service.Service) {
	if svc.Load("hello") == "" {
		fmt.Println("missing")
	}
}
