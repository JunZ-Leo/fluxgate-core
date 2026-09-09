FROM alpine:latest as builder
ARG TARGETPLATFORM
RUN echo "I'm building for $TARGETPLATFORM"

RUN apk add --no-cache gzip && \
    mkdir /fluxgate-config && \
    wget -O /fluxgate-config/geoip.metadb https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.metadb && \
    wget -O /fluxgate-config/geosite.dat https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat && \
    wget -O /fluxgate-config/geoip.dat https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat

COPY docker/file-name.sh /fluxgate/file-name.sh
WORKDIR /fluxgate
COPY bin/ bin/
RUN FILE_NAME="$(sh file-name.sh)" && \
    gzip -dc "$FILE_NAME" > fluxgate && chmod +x fluxgate
FROM alpine:latest
LABEL org.opencontainers.image.source="https://github.com/JunZ-Leo/fluxgate-core"

RUN apk add --no-cache ca-certificates tzdata iptables

VOLUME ["/root/.config/fluxgate/"]

COPY --from=builder /fluxgate-config/ /root/.config/fluxgate/
COPY --from=builder /fluxgate/fluxgate /fluxgate
ENTRYPOINT [ "/fluxgate" ]
