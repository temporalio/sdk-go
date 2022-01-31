package a

import "time"

type SomeStruct struct{}

func (SomeStruct) NonPtrReceiver() { // want NonPtrReceiver:"calls non-deterministic function time\\.Now"
	time.Now()
}

func (*SomeStruct) PtrReceiver() { // want PtrReceiver:"calls non-deterministic function time\\.Now"
	time.Now()
}

type SomeInterface interface {
	BadCall() // want BadCall:"declared non-deterministic"
}

func CallsNonPtrReceiver() { // want CallsNonPtrReceiver:"calls non-deterministic function \\(a\\.SomeStruct\\).NonPtrReceiver"
	SomeStruct{}.NonPtrReceiver()
}

func CallsPtrReceiver() { // want CallsPtrReceiver:"calls non-deterministic function \\(\\*a\\.SomeStruct\\)\\.PtrReceiver"
	var s SomeStruct
	s.PtrReceiver()
}

func CallsInterface() { // want CallsInterface:"calls non-deterministic function \\(a\\.SomeInterface\\)\\.BadCall"
	var iface SomeInterface
	iface.BadCall()
}
