FROM golang:1.15

RUN mkdir -p /go/src/go.temporal.io/sdk
WORKDIR /go/src/go.temporal.io/sdk

ADD go.mod go.sum /go/src/go.temporal.io/sdk/
RUN go mod download
