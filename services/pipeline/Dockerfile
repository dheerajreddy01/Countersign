# One image for every binary in this module: they share the same internal
# packages, so building them separately would download and compile the same
# dependencies four times.
#
# All four are built in and the caller picks one as the command
# (compose: `command: ["/caseservice"]`, Render: `dockerCommand: /caseservice`).
# An earlier version selected the binary with `--build-arg CMD=`, which ties the
# image to a platform passing build args through reliably — not something every
# host does, and a silent empty value would build nothing at all.
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -o /out/normalizer ./cmd/normalizer \
    && CGO_ENABLED=0 go build -o /out/caseservice ./cmd/caseservice \
    && CGO_ENABLED=0 go build -o /out/mandate ./cmd/mandate \
    && CGO_ENABLED=0 go build -o /out/demoagent ./cmd/demoagent

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/normalizer /normalizer
COPY --from=build /out/caseservice /caseservice
COPY --from=build /out/mandate /mandate
COPY --from=build /out/demoagent /demoagent
# No default command on purpose: a default would let a misconfigured service
# start the wrong binary and look healthy while consuming nothing.
