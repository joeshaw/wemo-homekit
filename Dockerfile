FROM golang:alpine as builder

RUN echo "nobody:x:1:1:nobody:/:/bin/sh" >> /etc/passwd
RUN echo "nobody:x:1:" >> /etc/group

RUN apk update && apk add --no-cache ca-certificates git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -installsuffix 'static' -o /usr/local/bin/wemo-homekit .

FROM scratch

COPY --from=builder /etc/passwd /etc/group /etc/
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/local/bin/wemo-homekit /usr/local/bin/

USER nobody:nobody
ENTRYPOINT ["/usr/local/bin/wemo-homekit"] 
