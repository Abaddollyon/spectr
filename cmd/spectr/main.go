package main

import (
	"fmt"
	"os"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("spectr %s\n", version)
		return
	}

	fmt.Println("spectr - agent observability engine")
	fmt.Printf("version %s\n", version)
	// TODO: wire up config, store, collectors, API, dashboard
}
