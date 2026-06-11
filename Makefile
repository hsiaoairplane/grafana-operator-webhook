.PHONY: all
all: test build

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -race ./...

.PHONY: build
build:
	go build -o grafana-operator-webhook -v .

.PHONY: clean
clean:
	go clean ./...
