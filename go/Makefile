BIN  := moo-cloud
PKGS := ./cmd/moo-cloud

.PHONY: build dist test

build:
	go build -o ../dist/$(BIN)-$(shell go env GOOS)-$(shell go env GOARCH) $(PKGS)

dist:
	mkdir -p ../dist
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/$(BIN)-darwin-arm64  $(PKGS)
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/$(BIN)-darwin-amd64  $(PKGS)
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/$(BIN)-linux-amd64   $(PKGS)
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/$(BIN)-linux-arm64   $(PKGS)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/$(BIN)-windows-amd64.exe $(PKGS)

test:
	go test ./...
