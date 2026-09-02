VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build linux-amd64 test vet clean

build:
	mkdir -p dist
	go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ciru-strixlink ./cmd/ciru-strixlink

linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ciru-strixlink-linux-amd64 ./cmd/ciru-strixlink

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf dist
