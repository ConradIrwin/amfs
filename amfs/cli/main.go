package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/automerge/automerge-go"
)

func main() {

	bytes, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	doc, err := automerge.Load(bytes)
	if err != nil {
		panic(err)
	}

	fmt.Println(doc.Root().Interface())
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	e.Encode(doc.Root().Interface())
}
