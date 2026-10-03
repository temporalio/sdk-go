FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY . .
WORKDIR /src/contrib/gcp
RUN go build -trimpath -ldflags="-s -w" -o /out/sdk-go-otel-e2e ./e2e

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/sdk-go-otel-e2e /sdk-go-otel-e2e
ENTRYPOINT ["/sdk-go-otel-e2e"]
