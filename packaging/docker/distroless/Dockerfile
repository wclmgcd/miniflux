FROM docker.io/library/golang:trixie AS build
ADD . /go/src/app
WORKDIR /go/src/app
# 版本号：优先使用 --build-arg VERSION=xxx，未传入时回退到当前 git tag
ARG VERSION
RUN make miniflux VERSION="${VERSION:-$(git describe --tags --exact-match 2>/dev/null || true)}" \
 && mkdir -p /var/lib/miniflux

FROM gcr.io/distroless/base-debian13:nonroot

LABEL org.opencontainers.image.title=Miniflux
LABEL org.opencontainers.image.description="Miniflux is a minimalist and opinionated feed reader"
LABEL org.opencontainers.image.vendor="Frédéric Guillot"
LABEL org.opencontainers.image.licenses=Apache-2.0
LABEL org.opencontainers.image.url=https://miniflux.app
LABEL org.opencontainers.image.source=https://github.com/miniflux/v2
LABEL org.opencontainers.image.documentation=https://miniflux.app/docs/

EXPOSE 8080
ENV LISTEN_ADDR=0.0.0.0:8080
COPY --from=build /go/src/app/miniflux /usr/bin/miniflux
# distroless nonroot 的 uid 是 65532，媒体缓存目录要提前建好并改属主
COPY --from=build --chown=65532:65532 /var/lib/miniflux /var/lib/miniflux
CMD ["/usr/bin/miniflux"]
