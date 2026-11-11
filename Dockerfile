# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build static stripped binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /atlas ./cmd/atlas

# Final lean runtime stage (<25MB)
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=America/Sao_Paulo

WORKDIR /app

# Create data directory for SQLite persistence
RUN mkdir -p /app/data

COPY --from=builder /atlas /app/atlas

EXPOSE 8080

VOLUME ["/app/data"]

CMD ["/app/atlas"]
