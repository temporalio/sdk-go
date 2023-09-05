package a

func StartsGoroutine() { // want StartsGoroutine:"starts goroutine"
	go func() {}()
}

func StartsGoroutineTransitively() { // want StartsGoroutineTransitively:"calls non-deterministic function a.StartsGoroutine"
	StartsGoroutine()
}

func ChannelSend() { // want ChannelSend:"sends to channel"
	ch := make(chan string, 1)
	ch <- "foo"
}

func ChannelSendInSelect() { // want ChannelSendInSelect:"sends to channel"
	ch := make(chan string, 1)
	select {
	case ch <- "foo":
	default:
	}
}

func ChannelRecv() { // want ChannelRecv:"receives from channel"
	ch := make(chan string, 1)
	<-ch
}

func ChannelRecvAssign() { // want ChannelRecvAssign:"receives from channel"
	ch := make(chan string, 1)
	_ = <-ch
}

func ChannelRecvInSelect() { // want ChannelRecvInSelect:"receives from channel"
	ch := make(chan string, 1)
	select {
	case <-ch:
	default:
	}
}

func ChannelRange() { // want ChannelRange:"iterates over channel"
	ch := make(chan string, 1)
	for range ch {
	}
}

type MyChan chan string

func ChannelRangeWrappedType() { // want ChannelRangeWrappedType:"iterates over channel"
	ch := make(MyChan, 1)
	for range ch {
	}
}

func ChannelRangeGenericType[C chan T, T any](ch C) { // want ChannelRangeGenericType:"iterates over channel"
	for range ch {
	}
}
