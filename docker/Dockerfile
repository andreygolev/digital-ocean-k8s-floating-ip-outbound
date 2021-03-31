FROM golang as builder

RUN mkdir -p /app
WORKDIR /app

ENV GO111MODULE=on
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go install ./...

FROM alpine
COPY --chown=0:0 --from=builder /go/bin/digital-ocean-floating-ip-manager /bin/digital-ocean-floating-ip-manager

ENTRYPOINT [ "/bin/digital-ocean-floating-ip-manager" ]
