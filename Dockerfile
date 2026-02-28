FROM oven/bun:alpine AS bun-source

FROM golang:1-alpine

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=bun-source /usr/local/bin/bun /usr/local/bin/bun
RUN ln -s /usr/local/bin/bun /usr/local/bin/bunx

WORKDIR /app

EXPOSE 5001

ENV PORT=5001

ENTRYPOINT ["sh"]
CMD ["./run.sh"]
