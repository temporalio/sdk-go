package a

import "fmt"

func IgnoreSameLine() { // want IgnoreSameLine:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	fmt.Print("Do not ignore this")
	fmt.Printf("Ignore this") //workflowcheck:ignore
	fmt.Println("Do not ignore this")
}
