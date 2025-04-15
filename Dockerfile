FROM golang:1.24.2 AS go
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOBIN=/bin
#ADD https://github.com/spiffe/spire/releases/download/v1.2.2/spire-1.2.2-linux-x86_64-glibc.tar.gz .
#RUN tar xzvf spire-1.2.2-linux-x86_64-glibc.tar.gz -C /bin --strip=2 spire-1.2.2/bin/spire-server spire-1.2.2/bin/spire-agent

FROM go AS build
ARG TARGETPLATFORM
ARG TARGETARCH
ARG TARGETOS

WORKDIR /build
COPY go.mod go.sum ./
ADD vendor vendor
COPY . .
RUN go env -w GOPRIVATE=github.com/kubeslice && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on go build -mod=vendor -a -o /bin/kernel-forwarder .

FROM alpine:3.21.3 AS runtime
COPY --from=build /bin/kernel-forwarder /bin/kernel-forwarder

ENTRYPOINT ["/bin/kernel-forwarder"]

