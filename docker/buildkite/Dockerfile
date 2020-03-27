FROM golang:1.14

RUN mkdir -p /go/src/go.temporal.io/temporal
WORKDIR /go/src/go.temporal.io/temporal

ADD go.mod go.sum /go/src/go.temporal.io/temporal/
RUN go mod download
