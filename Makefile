.PHONY: build server cli test tidy run clean docker docker-run linux fmt vet

build: server cli

server:
	go build -o quick-server .

cli:
	go build -o quick ./cli

tidy:
	go mod tidy

test:
	go test ./...

run: server
	./quick-server

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o quick-server .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o quick ./cli

# Skinniest image (scratch + static binary + CA certs)
docker:
	docker build -t quick:slim .

docker-run:
	docker run --rm -p 8080:8080 -v quick-data:/data quick:slim

clean:
	rm -f quick-server quick quick-server.exe quick.exe

fmt:
	go fmt ./...

vet:
	go vet ./...
