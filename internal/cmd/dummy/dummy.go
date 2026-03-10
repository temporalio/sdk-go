// This file exists to force compilation of all code that doesn't have unit tests.
package main

import (
	_ "go.temporal.io/sdk/activity"
	_ "go.temporal.io/sdk/client"
	_ "go.temporal.io/sdk/converter"
	_ "go.temporal.io/sdk/log"
	_ "go.temporal.io/sdk/temporal"
	_ "go.temporal.io/sdk/testsuite"
	_ "go.temporal.io/sdk/worker"
	_ "go.temporal.io/sdk/workflow"
)

func main() {
}
