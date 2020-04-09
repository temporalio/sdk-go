// This file exists to force compilation of all code that doesn't have unit tests.
package main

import (
	_ "go.temporal.io/temporal"
	_ "go.temporal.io/temporal/activity"
	_ "go.temporal.io/temporal/client"
	_ "go.temporal.io/temporal/encoded"
	_ "go.temporal.io/temporal/testsuite"
	_ "go.temporal.io/temporal/worker"
	_ "go.temporal.io/temporal/workflow"
)

func main() {
}
