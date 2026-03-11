FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /spectr ./cmd/spectr

FROM scratch
COPY --from=builder /spectr /spectr
EXPOSE 9099
ENTRYPOINT ["/spectr"]
