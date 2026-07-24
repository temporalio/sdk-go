package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("expected collect-ci, capture-patch, collect-outcomes, render, or stats")
	}

	var err error
	switch os.Args[1] {
	case "collect-ci":
		err = runCollectCI(os.Args[2:])
	case "capture-patch":
		err = runCapturePatch(os.Args[2:])
	case "collect-outcomes":
		err = runCollectOutcomes(os.Args[2:])
	case "render":
		err = runRender(os.Args[2:])
	case "stats":
		err = runStats(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%s", sanitizeText(err.Error(), 1600))
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func requiredString(fs *flag.FlagSet, name, description string) *string {
	return fs.String(name, "", description)
}

func requireFlags(values map[string]string) error {
	for name, value := range values {
		if value == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}
	return nil
}
