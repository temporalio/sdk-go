FROM golang:1.12.1

RUN go get -u github.com/golang/dep/cmd/dep \
  && go get -u github.com/Masterminds/glide \
  && go get -u golang.org/x/lint/golint

RUN mkdir -p /go/src/go.temporal.io/temporal
WORKDIR /go/src/go.temporal.io/temporal

ADD go.mod go.sum /go/src/go.temporal.io/temporal/
RUN GO111MODULE=on go mod download
