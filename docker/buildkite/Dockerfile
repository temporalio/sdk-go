FROM golang:1.12.1

RUN go get -u github.com/golang/dep/cmd/dep \
  && go get -u github.com/Masterminds/glide \
  && go get -u golang.org/x/lint/golint

RUN mkdir -p /go/src/go.uber.org/cadence
WORKDIR /go/src/go.uber.org/cadence

ADD go.mod go.sum /go/src/go.uber.org/cadence/
RUN GO111MODULE=on go mod download
