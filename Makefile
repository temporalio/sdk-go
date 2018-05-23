.PHONY: test bins clean cover cover_ci

# default target
default: test

IMPORT_ROOT := go.uber.org/cadence
THRIFT_GENDIR := .gen/go
THRIFTRW_SRC := idl/github.com/uber/cadence/cadence.thrift
# one or more thriftrw-generated file(s), to create / depend on generated code
THRIFTRW_OUT := $(THRIFT_GENDIR)/cadence/idl.go
TEST_ARG ?= -coverprofile=$(BUILD)/cover.out -race

# general build-product folder, cleaned as part of `make clean`
BUILD := .build
# general bins folder.  NOT cleaned via `make clean`
BINS := .bins

# Automatically gather all srcs + a "sentinel" thriftrw output file (which forces generation).
ALL_SRC := $(THRIFTRW_OUT) $(shell \
	find . -name "*.go" | \
	grep -v \
	-e vendor/ \
	-e .gen/ \
	-e .build/ \
)

# Files that needs to run lint.  excludes testify mocks and the thrift sentinel.
LINT_SRC := $(filter-out ./mock% $(THRIFTRW_OUT),$(ALL_SRC))

THRIFTRW_VERSION := v1.11.0
YARPC_VERSION := v1.29.1
GOLINT_VERSION := 470b6b0bb3005eda157f0275e2e4895055396a81

# versioned tools.  just change the version vars above, it'll automatically trigger a rebuild.
$(BINS)/versions/thriftrw-$(THRIFTRW_VERSION):
	./versioned_go_build.sh go.uber.org/thriftrw $(THRIFTRW_VERSION) $@

$(BINS)/versions/yarpc-$(YARPC_VERSION):
	./versioned_go_build.sh go.uber.org/yarpc $(YARPC_VERSION) encoding/thrift/thriftrw-plugin-yarpc $@

$(BINS)/versions/golint-$(GOLINT_VERSION):
	./versioned_go_build.sh golang.org/x/lint $(GOLINT_VERSION) golint $@

# stable tool targets.  depend on / execute these instead of the versioned ones.
# this versioned-to-nice-name thing is mostly because thriftrw depends on the yarpc
# bin to be named "thriftrw-plugin-yarpc".
$(BINS)/thriftrw: $(BINS)/versions/thriftrw-$(THRIFTRW_VERSION)
	@ln -fs $(CURDIR)/$< $@

$(BINS)/thriftrw-plugin-yarpc: $(BINS)/versions/yarpc-$(YARPC_VERSION)
	@ln -fs $(CURDIR)/$< $@

$(BINS)/golint: $(BINS)/versions/golint-$(GOLINT_VERSION)
	@ln -fs $(CURDIR)/$< $@

vendor: vendor/glide.updated

vendor/glide.updated: glide.lock
	glide install
	touch vendor/glide.updated

$(THRIFTRW_OUT): $(THRIFTRW_SRC) $(BINS)/thriftrw $(BINS)/thriftrw-plugin-yarpc
	@echo 'thriftrw: $(THRIFTRW_SRC)'
	@mkdir -p $(dir $@)
	@# needs to be able to find the thriftrw-plugin-yarpc bin in PATH
	@PATH="$(BINS)" \
		$(BINS)/thriftrw \
		--plugin=yarpc \
		--pkg-prefix=$(IMPORT_ROOT)/$(THRIFT_GENDIR) \
		--out=$(THRIFT_GENDIR) \
		$(THRIFTRW_SRC)

clean_thrift:
	rm -rf .gen

# `make copyright` or depend on "copyright" to force-run licensegen,
# or depend on $(BUILD)/copyright to let it run as needed.
copyright $(BUILD)/copyright: $(ALL_SRC)
	@mkdir -p $(BUILD)
	go run ./internal/cmd/tools/copyright/licensegen.go --verifyOnly
	@touch $(BUILD)/copyright

$(BUILD)/dummy: vendor/glide.updated $(ALL_SRC)
	go build -i -o $@ internal/cmd/dummy/dummy.go

test $(BUILD)/cover.out: $(BUILD)/copyright $(BUILD)/dummy $(ALL_SRC)
	go test ./... $(TEST_ARG)

bins: $(ALL_SRC) $(BUILD)/copyright lint $(BUILD)/dummy

cover: $(BUILD)/cover.out
	go tool cover -html=$(BUILD)/cover.out;

cover_ci: $(BUILD)/cover.out
	goveralls -coverprofile=$(BUILD)/cover.out -service=travis-ci || echo -e "\x1b[31mCoveralls failed\x1b[m";

# golint fails to report many lint failures if it is only given a single file
# to work on at a time, and it can't handle multiple packages at once, *and*
# we can't exclude files from its checks, so for best results we need to give
# it a whitelist of every file in every package that we want linted, per package.
#
# so lint + this golint func works like:
# - iterate over all lintable dirs (outputs "./folder/")
# - find .go files in a dir (via wildcard, so not recursively)
# - filter to only files in LINT_SRC
# - if it's not empty, run golint against the list
define lint_if_present
test -n "$1" && $(BINS)/golint -set_exit_status $1
endef

lint: $(BINS)/golint $(ALL_SRC)
	$(foreach pkg,\
		$(sort $(dir $(LINT_SRC))), \
		$(call lint_if_present,$(filter $(wildcard $(pkg)*.go),$(LINT_SRC))) || ERR=1; \
	) test -z "$$ERR" || exit 1
	@OUTPUT=`gofmt -l $(ALL_SRC) 2>&1`; \
	if [ "$$OUTPUT" ]; then \
		echo "Run 'make fmt'. gofmt must be run on the following files:"; \
		echo "$$OUTPUT"; \
		exit 1; \
	fi

fmt:
	@gofmt -w $(ALL_SRC)

clean:
	rm -Rf $(BUILD)
	rm -Rf .gen
