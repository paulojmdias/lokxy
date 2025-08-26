FROM debian:13-slim AS builder

ARG BUILDPLATFORM
ARG TARGETARCH
ARG TARGETOS
ARG LOKXY_VERSION
ENV GOARCH=${TARGETARCH} GOOS=${TARGETOS}

RUN apt update &&\
    apt install -y --no-install-recommends --no-install-suggests \
        ca-certificates \
        curl &&\
    curl -sL -o /tmp/lokxy_${LOKXY_VERSION}_${TARGETOS}_${TARGETARCH}.tar.gz https://github.com/paulojmdias/lokxy/releases/download/${LOKXY_VERSION}/lokxy_${LOKXY_VERSION}_${TARGETOS}_${TARGETARCH}.tar.gz &&\
    tar -xvf /tmp/lokxy_${LOKXY_VERSION}_${TARGETOS}_${TARGETARCH}.tar.gz -C /tmp

FROM gcr.io/distroless/static-debian12

EXPOSE 3100

COPY --from=builder /tmp/lokxy /usr/local/bin/lokxy

CMD ["/usr/local/bin/lokxy"]
