FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go index.html tclogo_default.png ./
RUN RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/tabellone .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/tabellone /app/tabellone
ENV TORNEO_DATA=/data TZ=Europe/Rome
EXPOSE 10000
CMD ["/app/tabellone"]
