# Same Dockerfile works for every Go service.

# ---- Stage 1: compile a static Go binary ----
FROM golang:1.27-alpine AS build
WORKDIR /src

# Copy only the dependency list first so this layer is cached and
# `go mod download` doesn't re-run every time you edit a .go file.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Version string baked into the binary (sent back in the X-App-Version header).
# Override with: docker build --build-arg APP_VERSION=v2 .
ARG APP_VERSION=v1

# CGO_ENABLED=0 -> fully static binary that needs no C libraries at runtime.
# -trimpath and -s -w strip local paths and debug info to shrink the binary.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${APP_VERSION}" \
    -o /out/service .

# ---- Stage 2: minimal runtime image (no shell, no package manager) ----
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/service /service
ENV GIN_MODE=release PORT=8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/service"]
