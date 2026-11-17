BIN := dyndns-updater

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/$(BIN) ./cmd/$(BIN)

test:
	go test ./...

install: build
	install -m 755 bin/$(BIN) /usr/local/bin/

clean:
	rm -rf bin

.PHONY: build test install clean
