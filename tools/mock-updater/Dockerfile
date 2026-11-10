# 模拟更新源（本地调试专用，不进发布物）
#
#   docker build -f tools/mock-updater/Dockerfile -t deskkit/mock-updater:dev .
#   docker run -d --name deskkit-mock -p 8790:8790 \
#     -e MOCK_VERSION=9.9.9 \
#     -e MOCK_PUBLIC_URL=http://<lan-host>:8790 \
#     deskkit/mock-updater:dev
#
# 为什么必须有 PUBLIC_URL：容器里生成资产下载地址时用的是"我自己的地址"，
# 填 127.0.0.1 的话 Mac / Windows 都取不到 —— 必须填局域网 IP。

FROM golang:1.26-alpine AS build
WORKDIR /src
# 只拷需要的部分，构建缓存更稳
COPY go.mod go.sum ./
RUN go mod download
# 只需要这两个目录：mock-updater 不依赖 internal
COPY tools/mock-updater ./tools/mock-updater
RUN CGO_ENABLED=0 go build -trimpath -o /out/mock-updater ./tools/mock-updater

FROM alpine:3.20
RUN adduser -D -u 10001 mock && mkdir -p /data && chown mock /data
COPY --from=build /out/mock-updater /usr/local/bin/mock-updater
USER mock
WORKDIR /data
ENV MOCK_VERSION=9.9.9 \
    MOCK_PORT=8790 \
    MOCK_PUBLIC_URL="" \
    MOCK_PLATFORMS=windows/amd64,darwin/amd64,darwin/arm64
EXPOSE 8790
# 用 shell 形式才能展开环境变量
CMD ["sh","-c","exec mock-updater --version \"$MOCK_VERSION\" --port \"$MOCK_PORT\" --dir /data --public-url \"$MOCK_PUBLIC_URL\" --platforms \"$MOCK_PLATFORMS\""]
