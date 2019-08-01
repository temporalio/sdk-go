# Go framework for Cadence [![Build Status](https://badge.buildkite.com/e7241785444519bdfd1defc68839fd19a89c15adb3477c73f7.svg?theme=github&branch=master)](https://buildkite.com/uberopensource/cadence-go-client) [![Coverage Status](https://coveralls.io/repos/uber-go/cadence-client/badge.svg?branch=master&service=github)](https://coveralls.io/github/uber-go/cadence-client?branch=master) [![GoDoc](https://godoc.org/go.uber.org/cadence?status.svg)](https://godoc.org/go.uber.org/cadence)

[Cadence](https://github.com/uber/cadence) is a distributed, scalable, durable, and highly available orchestration engine we developed at Uber Engineering to execute asynchronous long-running business logic in a scalable and resilient way.

`cadence-client` is the framework for authoring workflows and activities.

## How to use

Make sure you clone this repo into the correct location.

```bash
git clone git@github.com:uber-go/cadence-client.git $GOPATH/src/go.uber.org/cadence
```

or

```bash
go get go.uber.org/cadence
```

See [samples](https://github.com/uber-common/cadence-samples) to get started. 

Documentation is available [here](https://cadenceworkflow.io/docs/03_goclient/). 
You can also find the API documentation [here](https://godoc.org/go.uber.org/cadence).

## Contributing
We'd love your help in making the Cadence Go client great. Please review our [contribution guidelines](CONTRIBUTING.md).

## License
MIT License, please see [LICENSE](LICENSE) for details.

