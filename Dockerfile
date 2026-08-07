# The Stellar Go SDK requires Go 1.25.
FROM golang:1.25-alpine AS build

WORKDIR /src

# Download dependencies first so that source edits do not invalidate the layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/soroprobe ./cmd/soroprobe

FROM alpine:3.20

# SoroProbe talks to Stellar RPC over HTTPS and needs root certificates.
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 soroprobe

COPY --from=build /out/soroprobe /usr/local/bin/soroprobe

USER soroprobe
EXPOSE 8080

# SoroProbe is stateless and read-only; it holds no keys and writes no data.
ENTRYPOINT ["soroprobe"]
CMD ["serve", "--addr", ":8080"]
