FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o greenpartytracker .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /app/greenpartytracker .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/data ./data

ENV PORT=3000

EXPOSE 3000

CMD ["./greenpartytracker"]
