.PHONY: fmt vet test race vuln integration e2e build check macos-packaging-spike macos-odbc-packaging-spike macos-backend-smoke macos-app-unsigned macos-test macos-e2e macos-privacy-audit macos-accessibility-audit

fmt:
	test -z "$$(rg --files -g '*.go' | xargs gofmt -l)"

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

macos-packaging-spike:
	./scripts/macos-packaging-spike.sh

macos-odbc-packaging-spike:
	./scripts/macos-odbc-packaging-spike.sh

macos-backend-smoke:
	./scripts/macos-backend-smoke.sh

macos-app-unsigned:
	./scripts/package-macos-unsigned.sh

macos-test:
	xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS'

macos-e2e:
	xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidgeUITests -destination 'platform=macOS'

macos-privacy-audit:
	./scripts/macos-privacy-audit.sh

macos-accessibility-audit:
	./scripts/macos-accessibility-audit.sh
