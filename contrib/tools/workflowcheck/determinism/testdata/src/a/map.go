package a

import "fmt"

func MapIterate() { // want MapIterate:"iterates over map"
	var m map[string]string
	for k, v := range m {
		_ = fmt.Sprint(k, v)
	}
}

func CallsMapIterate() { // want CallsMapIterate:"calls non-deterministic function a.MapIterate"
	MapIterate()
}

type MyMap map[string]string

func MapIterateWrappedType() { // want MapIterateWrappedType:"iterates over map"
	var m MyMap
	for k, v := range m {
		_ = fmt.Sprint(k, v)
	}
}
