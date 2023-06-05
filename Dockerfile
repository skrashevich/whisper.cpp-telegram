# syntax = docker/dockerfile-upstream:master-labs

FROM alpine:latest AS xz
RUN apk add xz

FROM nvidia/cuda:12.1.0-runtime-ubuntu20.04 as cuda-builder
ARG DEBIAN_FRONTEND=noninteractive
# Prepare apt for buildkit cache
RUN rm -f /etc/apt/apt.conf.d/docker-clean \
  && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' >/etc/apt/apt.conf.d/keep-cache

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked --mount=type=cache,target=/var/lib/apt,sharing=locked <<EOT
apt update 
apt -y upgrade
apt install -y --allow-change-held-packages --no-install-recommends git wget unzip vim make gcc cuda-nvcc-12-1 libcublas-12-1 libcublas-dev-12-1
EOT
ENV PATH=$PATH:/usr/local/cuda-12.1/bin

FROM cuda-builder as whisper-builder
ADD https://github.com/ggerganov/whisper.cpp.git /whisper.cpp
WORKDIR /whisper.cpp
ENV WHISPER_CUBLAS=1
RUN make -j$(nproc)
RUN make libwhisper.a

FROM cuda-builder as go-builder
ADD https://go.dev/dl/go1.20.4.linux-amd64.tar.gz /tmp/go1.20.4.linux-amd64.tar.gz
RUN rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go1.20.4.linux-amd64.tar.gz
ENV PATH=$PATH:/usr/local/go/bin

FROM go-builder as build
COPY --link --from=whisper-builder /whisper.cpp /whisper.cpp
WORKDIR /whisper.cpp/bindings/go
ENV WHISPER_CUBLAS=1
ADD go.mod go.sum pkg/whisper/go.mod pkg/whisper/go.sum /app/
WORKDIR /app
RUN go mod download
ADD --link . /app/
ENV C_INCLUDE_PATH=/whisper.cpp

ENV CGO_LDFLAGS="-ldl -lrt -lm -lstdc++ -lcublas -lculibos -lcudart -lcublasLt -lwhisper -lpthread  -L/usr/local/cuda/lib64 -L/opt/cuda/lib64 -L/usr/local/cuda/targets/x86_64-linux/lib -L/whisper.cpp"
ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-DGGML_USE_CUBLAS -O3 -DNDEBUG -std=c11   -fPIC -pthread -mavx2 -mfma -mf16c -mavx -msse3 -Wno-error=implicit-function-declaration"
ENV CGO_CXXFLAGS="-DGGML_USE_CUBLAS -O3 -DNDEBUG -std=c++11 -fPIC -pthread -Wno-error=implicit-function-declaration"
ENV GODEBUG="cgocheck=0"
RUN go build -ldflags "-s -w" -trimpath -tags netgo

ENTRYPOINT [ "/app/whisper.cpp-telegram" ]

FROM xz as ffmpeg-downloader
ADD --link https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2022-07-31-12-37/ffmpeg-n5.1-2-g915ef932a3-linux64-gpl-5.1.tar.xz /tmp/btbn-ffmpeg.tar.xz
RUN tar -xf /tmp/btbn-ffmpeg.tar.xz -C /usr/local/ --strip-components 1

FROM nvidia/cuda:12.1.0-base-ubuntu20.04
ARG DEBIAN_FRONTEND=noninteractive
# Prepare apt for buildkit cache
RUN rm -f /etc/apt/apt.conf.d/docker-clean \
  && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' >/etc/apt/apt.conf.d/keep-cache

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked --mount=type=cache,target=/var/lib/apt,sharing=locked <<EOT
apt update 
apt install -y --no-install-recommends libcublas-12-1 cuda-cudart-12-1
EOT
COPY --link --from=ffmpeg-downloader /usr/local/bin/ffmpeg /usr/local/bin/
COPY --link --from=build /app/whisper.cpp-telegram /app/whisper.cpp-telegram 
ENTRYPOINT [ "/app/whisper.cpp-telegram" ]