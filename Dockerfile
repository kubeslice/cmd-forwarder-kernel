# syntax=docker/dockerfile:1.4
FROM --platform=$BUILDPLATFORM golang:1.22.5 as go
ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETARCH
ARG TARGETOS=linux
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOBIN=/bin
#ADD https://github.com/spiffe/spire/releases/download/v1.2.2/spire-1.2.2-linux-x86_64-glibc.tar.gz .
#RUN tar xzvf spire-1.2.2-linux-x86_64-glibc.tar.gz -C /bin --strip=2 spire-1.2.2/bin/spire-server spire-1.2.2/bin/spire-agent

FROM go as build
ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETARCH
ARG TARGETOS=linux
WORKDIR /build
COPY go.mod go.sum ./
ADD vendor vendor
COPY . .
RUN go env -w GOPRIVATE=github.com/kubeslice && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on go build -mod=vendor -a -o /bin/kernel-forwarder .

FROM --platform=$TARGETPLATFORM alpine:3.20.1 as runtime
ARG TARGETPLATFORM
ARG TARGETARCH
COPY --from=build /bin/kernel-forwarder /bin/kernel-forwarder

ENTRYPOINT ["/bin/kernel-forwarder"]

