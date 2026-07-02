# Regenerates the system Nexus client bindings in ../../workflow from the
# checked-in WIT service file.
#
# Generation runs nex-gen (located via NEX_GEN_BIN, else `nex-gen` on PATH,
# else installed with `cargo install`) and requires a protobuf descriptor set
# (FileDescriptorSet) covering the Temporal API. By default the descriptor is
# produced with `buf build` from the buf.build/temporalio/api module; override
# with DESCRIPTORS=/path/to/set.pb to use a prebuilt descriptor (e.g.
# temporal-api-go's descriptor_set.pb).
#
# Usage:
#   make -C internal/nexussystem generate
#   NEX_GEN_BIN=/path/to/nex-gen make -C internal/nexussystem generate
#   WIT_INPUT=/path/to/service.wit make -C internal/nexussystem generate
#   DESCRIPTORS=../../../temporal-api-go/descriptor_set.pb make -C internal/nexussystem generate

REPO_ROOT := ../..
WIT_INPUT ?= $(CURDIR)/wit/workflow-service.wit
DESCRIPTORS ?= $(CURDIR)/.descriptor_set.pb

.PHONY: generate descriptor install-buf clean

generate: $(DESCRIPTORS)
	cd $(REPO_ROOT) && go run ./internal/cmd/nexussystemgen -input $(WIT_INPUT) -descriptors $(DESCRIPTORS)

# Build a descriptor set from the published Temporal API buf module when the
# caller did not supply one.
$(CURDIR)/.descriptor_set.pb:
	buf build buf.build/temporalio/api --as-file-descriptor-set -o $@

install-buf:
	go install github.com/bufbuild/buf/cmd/buf@v1.27.0

clean:
	rm -f $(CURDIR)/.descriptor_set.pb
