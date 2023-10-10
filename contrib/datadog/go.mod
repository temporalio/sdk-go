module go.temporal.io/sdk/contrib/datadog

go 1.16

require (
	github.com/stretchr/testify v1.8.4
	go.temporal.io/sdk v1.12.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.42.0
)

replace (
	go.temporal.io/api => github.com/tdeebswihart/temporal-api-go v0.0.0-20231010204526-910c300e64c5
	go.temporal.io/sdk => ../../
)
