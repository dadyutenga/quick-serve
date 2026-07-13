.PHONY: build server cli test tidy run clean

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
	GOOS=linux GOARCH=amd64 go build -o quick-server .
	GOOS=linux GOARCH=amd64 go build -o quick ./cli

clean:
	rm -f quick-server quick quick-server.exe quick.exe

fmt:
	go fmt ./...

vet:
	go vet ./...
