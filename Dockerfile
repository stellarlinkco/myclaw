FROM golang:1.25.5-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /myclaw ./cmd/myclaw

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /myclaw /usr/local/bin/myclaw

RUN mkdir -p /root/.myclaw/workspace

VOLUME ["/root/.myclaw"]

EXPOSE 18790 9876 9886

ENTRYPOINT ["myclaw"]
CMD ["gateway"]
