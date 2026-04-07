binary := "ws"

# Build the ws binary
build:
    GOTOOLCHAIN=auto go build -o {{binary}} ./cmd/ws

# Build and install to ~/.local/bin
install: build
    cp {{binary}} ~/.local/bin/{{binary}}

# Remove built binary
clean:
    rm -f {{binary}}

# Build and run with args
run *args: build
    ./{{binary}} {{args}}
