// Command hub is the scan hub binary (stockbot hub).
package main

import (
	"os"

	"github.com/Rhionin/stockbot/internal/hub"
)

func main() {
	os.Exit(hub.Main(os.Args[1:]))
}
