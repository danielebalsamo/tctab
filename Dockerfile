FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copia le dipendenze
COPY go.mod ./
RUN go mod download

# Copia tutto il codice sorgente
COPY . .

# Compila l'applicazione Go
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/tabellone .

# Stage finale leggero
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /out/tabellone /app/tabellone
COPY --from=builder /app/index.html /app/index.html
COPY --from=builder /app/tclogo_default.png /app/tclogo_default.png

EXPOSE 8080

CMD ["/app/tabellone"]