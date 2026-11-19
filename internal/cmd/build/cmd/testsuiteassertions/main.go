package main

import (
	"go.temporal.io/sdk/internal/cmd/build/testsuiteassertions"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(testsuiteassertions.Analyzer)
}
