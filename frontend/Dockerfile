# Stage 1: Compile Tailwind CSS
FROM node:22-alpine AS css-builder
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY assets ./assets
COPY views ./views
COPY static ./static
RUN npx @tailwindcss/cli -i ./assets/css/input.css -o ./static/css/style.css

# Stage 2: Generate Templ & Build Go SSR Binary
FROM golang:1.26-alpine AS go-builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate && \
    CGO_ENABLED=0 GOOS=linux go build -o /app/frontend ./cmd/app

# Stage 3: Runtime
FROM alpine:3.20
WORKDIR /app

COPY --from=go-builder /app/frontend /app/frontend
COPY --from=css-builder /app/static /app/static

EXPOSE 3000
CMD ["/app/frontend"]
