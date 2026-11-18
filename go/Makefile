BIN     := moo-cloud
PKGS    := ./cmd/moo-cloud
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build dist meta test

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-$(shell go env GOOS)-$(shell go env GOARCH) $(PKGS)

meta:
	python3 build-meta.py $(VERSION)

dist:
	mkdir -p ../dist
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-darwin-arm64  $(PKGS)
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-darwin-amd64  $(PKGS)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-linux-amd64   $(PKGS)
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-linux-arm64   $(PKGS)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-windows-amd64.exe $(PKGS)
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../dist/$(BIN)-windows-arm64.exe $(PKGS)
	cd ../dist && shasum -a 256 moo-cloud-* > SHA256SUMS

test:
	go test ./...
