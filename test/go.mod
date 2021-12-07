module go.temporal.io/sdk/test

go 1.16

require (
	github.com/golang/mock v1.6.0
	github.com/pborman/uuid v1.2.1
	github.com/stretchr/testify v1.7.0
	github.com/uber-go/tally/v4 v4.1.1
	go.temporal.io/api v1.5.0
	go.temporal.io/sdk v1.11.1
	go.temporal.io/sdk/contrib/tally v0.1.0
	go.uber.org/goleak v1.1.11
)

replace go.temporal.io/sdk => ../

replace go.temporal.io/sdk/contrib/tally => ../contrib/tally
