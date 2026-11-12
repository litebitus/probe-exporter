COVERAGE_PROFILE = /tmp/probe-exporter-coverage.out
COVERAGE_MINIMUM = 95

.PHONY: build test

build: test
	go build -o bin/probe-exporter .

test:
	go vet ./...
	@unformatted="$$(gofmt -l .)"; [ -z "$$unformatted" ] || { echo "not gofmt-formatted:"; echo "$$unformatted"; exit 1; }
	go test -race -count=1 -coverpkg=./... -coverprofile=$(COVERAGE_PROFILE) ./...
	@go tool cover -func=$(COVERAGE_PROFILE) | awk -v minimum=$(COVERAGE_MINIMUM) '/^total:/ { \
		sub("%", "", $$3); \
		if ($$3 + 0 < minimum) { printf "\033[31mcoverage %s%%, below %s%%\033[0m\n", $$3, minimum; exit 1 } \
		printf "coverage %s%%, at least %s%%\n", $$3, minimum }'
