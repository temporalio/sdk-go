.PHONY: proto install-buf

# Regenerate protobuf Go code from .proto files.
# Requires buf to be installed (run `make install-buf` first).
proto:
	buf generate

install-buf:
	go install github.com/bufbuild/buf/cmd/buf@v1.27.0
