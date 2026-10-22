.PHONY: dev build

dev:
	@echo "Menjalankan Development Mode..."
	templ generate --watch & \
	npx @tailwindcss/cli -i ./assets/css/input.css -o ./static/css/style.css --watch & \
	air

build:
	templ generate
	npx @tailwindcss/cli -i ./assets/css/input.css -o ./static/css/style.css --minify
	go build -o bin/app cmd/app/main.go