FROM python:3.12-slim

RUN apt-get update && apt-get install -y \
    curl \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

RUN pip install uv

WORKDIR /app

COPY frontend/package*.json ./frontend/
RUN cd frontend && npm install

COPY pyproject.toml ./
RUN uv pip install --system --no-cache-dir -e .

COPY . .

RUN chmod +x start.sh

CMD ["./start.sh"]
