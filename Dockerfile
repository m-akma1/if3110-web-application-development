# Stage 1: Build Frontend SPA
FROM node:22-alpine AS frontend-builder
WORKDIR /app/frontend

COPY frontend/package.json ./
RUN npm install
COPY frontend/ ./
RUN npm run build

# Stage 2: Build Go Binary
FROM golang:1.26-alpine AS go-builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server .

# Stage 3: Runtime
FROM alpine:3.20
WORKDIR /app

COPY --from=go-builder /app/server /app/server
COPY --from=frontend-builder /app/frontend/dist /app/frontend/dist
COPY static /app/static

EXPOSE 8080
CMD ["/app/server"]
