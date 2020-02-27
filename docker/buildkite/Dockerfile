FROM golang:1.13.3

RUN go get -u golang.org/x/lint/golint

RUN mkdir -p /go/src/go.temporal.io/temporal
WORKDIR /go/src/go.temporal.io/temporal

ADD go.mod go.sum /go/src/go.temporal.io/temporal/
RUN GO111MODULE=on go mod download
