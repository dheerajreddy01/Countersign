# One image definition for every binary in this module: they share the same
# internal packages, so building them separately would download and compile the
# same dependencies four times. Pick one with --build-arg CMD=<name>.
FROM golang:1.22 AS build
ARG CMD
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
RUN test -n "$CMD" || (echo "build arg CMD is required (normalizer|caseservice|mandate|demoagent)" && exit 1)
RUN CGO_ENABLED=0 go build -o /service ./cmd/$CMD

FROM gcr.io/distroless/static-debian12
COPY --from=build /service /service
ENTRYPOINT ["/service"]
