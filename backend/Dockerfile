# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /app

COPY go.mod ./
# Jika nantinya ada go.sum, copy juga:
# COPY go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/api ./cmd/api

# Runtime stage
FROM alpine:3.20
WORKDIR /app

COPY --from=builder /app/api /app/api

EXPOSE 8080
CMD ["/app/api"]
