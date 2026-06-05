# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o daedalus-api .

# ── Runtime stage ──────────────────────────────────────────────────────────────
FROM golang:1.25-alpine

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/daedalus-api .

EXPOSE 8000
ENTRYPOINT ["./daedalus-api"]
