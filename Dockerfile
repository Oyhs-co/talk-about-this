# --- Etapa de compilacion ---
FROM golang:1.27-alpine AS builder

WORKDIR /src

# Copiar primero los modulos para aprovechar la cache de capas de Docker
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Compilacion estatica y sin trazas de depuracion
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w" \
    -o /out/talkaboutthis \
    ./cmd/talkaboutthis

# --- Etapa de ejecucion ---
FROM alpine:3.20

# ca-certificates es necesario para las llamadas HTTPS a las APIs SaaS y a GitHub
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 talkaboutthis

COPY --from=builder /out/talkaboutthis /usr/local/bin/talkaboutthis

# El esquema de extraccion viaja embebido en tiempo de compilacion,
# por lo que debe existir en la imagen final.
COPY docs/specifications/backlog_schema.json /etc/talkaboutthis/backlog_schema.json

USER talkaboutthis
WORKDIR /work

ENTRYPOINT ["/usr/local/bin/talkaboutthis"]
CMD ["--help"]