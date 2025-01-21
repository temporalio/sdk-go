package a

import "fmt"

func IgnoreSameLine() { // want IgnoreSameLine:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	fmt.Print("Do not ignore this")
	fmt.Printf("Ignore this") //workflowcheck:ignore
	fmt.Println("Do not ignore this")
}

func IgnoreAboveLine() { // want IgnoreAboveLine:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	fmt.Print("Do not ignore this")
	//workflowcheck:ignore
	fmt.Printf("Ignore this")
	fmt.Println("Do not ignore this")
}

// IgnoreEntireFunction can have a Godoc comment too
//
//workflowcheck:ignore
func IgnoreEntireFunction() {
	fmt.Print("Do not ignore this")
	fmt.Printf("Ignore this")
	fmt.Println("Do not ignore this")
}

func IgnoreFunctionTransitively() {
	IgnoreEntireFunction()
}

func IgnoreBlock() { // want IgnoreBlock:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	for i := 0; i < 1; i++ {
		fmt.Print("Do not ignore this")
	}
	//workflowcheck:ignore
	for i := 0; i < 1; i++ {
		fmt.Printf("Ignore this")
	}
	for i := 0; i < 1; i++ {
		fmt.Println("Do not ignore this")
	}
}
