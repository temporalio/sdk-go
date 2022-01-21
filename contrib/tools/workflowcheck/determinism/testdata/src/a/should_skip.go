package a

import "time"

func CallsTimeButShouldBeSkipped() {
	time.Now()
}
