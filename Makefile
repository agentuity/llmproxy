.PHONY: all lint test vet tidy vuln

all: test

lint:
	@echo "linting..."
	@go fmt ./...

vet:
	@echo "vetting..."
	@go vet ./...

tidy:
	@echo "tidying..."
	@go mod tidy

vuln:
	@echo "checking for vulnerabilities..."
	@govulncheck -show verbose ./...

test: tidy lint vet vuln
	@echo "testing..."
	@go test -v -count=1 -race ./...

