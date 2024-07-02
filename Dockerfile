FROM golang:1.18.2-alpine as go
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOBIN=/bin
ADD https://github.com/spiffe/spire/releases/download/v1.2.2/spire-1.2.2-linux-x86_64-glibc.tar.gz .
RUN tar xzvf spire-1.2.2-linux-x86_64-glibc.tar.gz -C /bin --strip=2 spire-1.2.2/bin/spire-server spire-1.2.2/bin/spire-agent

FROM go as build
WORKDIR /build
COPY go.mod go.sum ./
ADD vendor vendor
COPY . .
RUN go env -w GOPRIVATE=github.com/kubeslice && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -mod=vendor -a -o /bin/kernel-forwarder .

FROM alpine:3 as runtime
COPY --from=build /bin/kernel-forwarder /bin/kernel-forwarder

ENTRYPOINT ["/bin/kernel-forwarder"]

