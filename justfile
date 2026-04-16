default:
    @just --list | grep -v default

test *args:
    go test -timeout 2s -race {{ args }} ./...

lint:
    golangci-lint run ./...

fmt *args:
    go fmt {{ args }} ./...
