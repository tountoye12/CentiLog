FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG SERVICE
RUN test -n "$SERVICE" && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/centilog "./cmd/${SERVICE}"

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 centilog \
    && adduser -S -D -H -u 10001 -G centilog centilog
WORKDIR /app
COPY --from=build --chown=10001:10001 /out/centilog /usr/local/bin/centilog
COPY --from=build --chown=10001:10001 /src/configs /app/configs
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/centilog"]
