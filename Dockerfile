FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lastfm-osmium . \
 && mkdir /out/data

# Needs CA certificates for Osmium over TLS
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lastfm-osmium /lastfm-osmium
# So the mounted volume inherits writable permissions.
COPY --from=build --chown=nonroot:nonroot /out/data /data

ENV DATABASE_PATH=/data/lastfm-osmium.db
VOLUME /data
ENTRYPOINT ["/lastfm-osmium"]
