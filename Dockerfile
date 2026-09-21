ARG GO_IMAGE=docker.m.daocloud.io/library/golang:1.25-bookworm
ARG RUNTIME_IMAGE=docker.m.daocloud.io/library/alpine:3.22

FROM ${GO_IMAGE} AS build

WORKDIR /src

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-trimpath

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w" -o /out/chat-agent ./cmd/server

FROM ${RUNTIME_IMAGE}

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -H -u 65532 chatagent

COPY --from=build /out/chat-agent /usr/local/bin/chat-agent

ENV TZ=Asia/Shanghai \
    SERVER_PORT=9998

EXPOSE 9998

USER chatagent

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:9998/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/chat-agent"]
