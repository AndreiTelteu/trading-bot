FROM node:22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache gcc musl-dev go ca-certificates tzdata

WORKDIR /app

EXPOSE 5001

ENV PORT=5001

ENTRYPOINT ["sh"]
CMD ["./run.sh"]
