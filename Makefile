.PHONY: all generate css build run test clean

all: generate css run

generate:
	@echo "==> Generating Templ files..."
	templ generate

css:
	@echo "==> Compiling Tailwind CSS..."
	npx.cmd @tailwindcss/cli -i ./assets/css/input.css -o ./static/css/style.css

build: generate css
	@echo "==> Building application binary..."
	go build -o bin/app cmd/app/main.go

run:
	@echo "==> Running application..."
	go run cmd/app/main.go

test: generate
	@echo "==> Running Go tests..."
	go test -v ./...

clean:
	@echo "==> Cleaning build artifacts..."
	rm -rf bin/