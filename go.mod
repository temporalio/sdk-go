module go.temporal.io/sdk

go 1.16

require (
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/gogo/protobuf v1.3.2
	github.com/gogo/status v1.1.1
	github.com/golang/mock v1.6.0
	github.com/golang/protobuf v1.5.2
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/kr/text v0.2.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/pborman/uuid v1.2.1
	github.com/robfig/cron v1.2.0
	github.com/stretchr/testify v1.8.1
	go.temporal.io/api v1.18.1
	go.uber.org/atomic v1.9.0
	golang.org/x/time v0.1.0
	google.golang.org/grpc v1.53.0
	google.golang.org/protobuf v1.28.1
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
)

replace go.temporal.io/api => /mnt/chonky/dev/temporal/api-go
