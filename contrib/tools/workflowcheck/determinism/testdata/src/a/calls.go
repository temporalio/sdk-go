package a

import (
	"log"
	mathrand "math/rand"
	"net/http"
	"time"
)

func CallsTime() { // want CallsTime:"calls non-deterministic function time.Now"
	time.Now()
}

func CallsTimeTransitively() { // want CallsTimeTransitively:"calls non-deterministic function a.CallsTime"
	CallsTime()
}

func CallsOtherTimeCall() { // want CallsOtherTimeCall:"calls non-deterministic function time.Until"
	// Marked non-deterministic because it calls time.Now internally
	time.Until(time.Time{})
}

func Recursion() {
	Recursion()
}

func RecursionWithTimeCall() { // want RecursionWithTimeCall:"calls non-deterministic function a.CallsTimeTransitively"
	Recursion()
	CallsTimeTransitively()
}

func MultipleCalls() { // want MultipleCalls:"calls non-deterministic function time.Now, calls non-deterministic function a.CallsTime"
	time.Now()
	CallsTime()
}

func BadCall() { // want BadCall:"declared non-deterministic"
	Recursion()
}

func IgnoredCall() {
	time.Now()
}

func IgnoredCallTransitive() {
	IgnoredCall()
}

func CallsLog() { // want CallsLog:"calls non-deterministic function log.Println"
	log.Println()
}

func CallsMathRandom() { // want CallsMathRandom:"calls non-deterministic function math/rand.Int"
	mathrand.Int()
}

func CallsHTTP() { // want CallsHTTP:"calls non-deterministic function net/http.Get"
	http.Get("http://example.com")
}
