FROM docker.io/library/golang:alpine3.23 AS build
RUN apk add --no-cache build-base git make
ADD . /go/src/app
WORKDIR /go/src/app
# 版本号：优先使用 --build-arg VERSION=xxx，未传入时回退到当前 git tag
ARG VERSION
RUN make miniflux VERSION="${VERSION:-$(git describe --tags --exact-match 2>/dev/null || true)}"

FROM docker.io/library/alpine:3.24

LABEL org.opencontainers.image.title=Miniflux
LABEL org.opencontainers.image.description="Miniflux is a minimalist and opinionated feed reader"
LABEL org.opencontainers.image.vendor="Frédéric Guillot"
LABEL org.opencontainers.image.licenses=Apache-2.0
LABEL org.opencontainers.image.url=https://miniflux.app
LABEL org.opencontainers.image.source=https://github.com/miniflux/v2
LABEL org.opencontainers.image.documentation=https://miniflux.app/docs/

EXPOSE 8080
ENV LISTEN_ADDR=0.0.0.0:8080
RUN apk --no-cache add ca-certificates tzdata
COPY --from=build /go/src/app/miniflux /usr/bin/miniflux
# 预创建媒体缓存目录并交给非 root 用户，这样挂载数据卷后 MEDIA_CACHE_DIRECTORY 才有写权限
RUN mkdir -p /var/lib/miniflux && chown 65534:65534 /var/lib/miniflux
USER 65534
CMD ["/usr/bin/miniflux"]
