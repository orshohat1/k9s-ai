# ---  Build stage  ---
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Embed Copilot CLI binary.
RUN go tool bundler || true

RUN CGO_ENABLED=0 go build \
    -ldflags="-w -s" \
    -a -tags netgo \
    -o /bin/k9s-ai main.go

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /bin/k9s-ai /bin/k9s-ai

ENTRYPOINT [ "/bin/k9s-ai" ]
