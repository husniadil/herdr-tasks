.PHONY: build test test-full e2e install clean

BIN := bin/htask

build:
	@# A stale bin/ht from before the rename is worse than a missing binary: it
	@# is a frozen pre-rename build, and anything still pointed at it by
	@# absolute path — an MCP entry, a script — keeps being served yesterday's
	@# verb surface, with the skew warning going to a stderr nobody reads.
	@rm -f bin/ht
	go build -o $(BIN) ./cmd/htask

# Two gates, one suite.
#
#   make test       the loop, in seconds — the state machine, the store against
#                   a temp SQLite file, payload shapes, the error vocabulary.
#   make test-full  the gate before a commit — the above plus every case that
#                   starts the daemon, walks the socket, or drives the fake
#                   herdr, with -race and a cross-compile check of the other
#                   supported platform.
#
# This is a split, NOT a reduction: test-full still runs all of it. The reason
# to split is that the two answer different questions, and a check that costs
# a minute gets run less often than one that costs seconds.
#
# gofmt is checked rather than applied: a formatting fix belongs in the commit
# that caused it, not silently in whoever runs the gate next.
test:
	@unformatted=$$(gofmt -l .); \
	  [ -z "$$unformatted" ] || { echo "gofmt needed: $$unformatted"; exit 1; }
	go vet ./...
	go test -short ./...

test-full:
	@unformatted=$$(gofmt -l .); \
	  [ -z "$$unformatted" ] || { echo "gofmt needed: $$unformatted"; exit 1; }
	go vet ./...
	@# The OTHER supported platform, checked by compiling for it. Everything
	@# here is developed on one OS, and the parts that reach for a socket or a
	@# signal are exactly the parts that differ between them. vet rather than
	@# test: it type-checks tests too, so it catches the class without needing
	@# that OS to run on.
	GOOS=linux GOARCH=amd64 go vet ./...
	GOOS=darwin GOARCH=arm64 go vet ./...
	@# Layer 3's herdr-NEEDING half is not run here — it needs a real herdr —
	@# but it is compiled here, so the suite cannot rot behind its build tag.
	@# Its herdr-FREE half carries no build tag, so `go test` below runs it
	@# like any other package: the harness's own failure reporting decides
	@# whether every other layer-3 failure is legible, and it used to be
	@# compiled by this line and executed by nothing.
	go vet -tags e2e ./...
	go test -race ./...

# Layer 3 of §12.1: the SHIPPED binary against a real headless herdr server in
# a throwaway named session on private socket paths. It is out of `test-full`
# on purpose — CI and a machine without Herdr must still have a green gate —
# and it skips loudly, naming what was missing, rather than passing quietly.
e2e: build
	go test -tags e2e -count=1 -v ./internal/e2e/...

install:
	go install ./cmd/htask

clean:
	rm -rf bin dist coverage.out
