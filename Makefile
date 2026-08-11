.PHONY: fmt vet test race vuln integration e2e build check

fmt:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

vuln:
	govulncheck ./...

integration:
	go test -tags=integration ./...

e2e:
	go test -tags=e2e ./...

build:
	go build -o bin/quackridge ./cmd/quackridge

check: fmt vet test race build
