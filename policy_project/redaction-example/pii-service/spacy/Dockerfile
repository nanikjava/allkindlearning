FROM python:3.13-slim

COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

WORKDIR /app

# Install dependencies first (cached unless pyproject.toml/uv.lock change).
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev

COPY *.py ./
COPY custom_recognizers/ ./custom_recognizers/

ENV PATH="/app/.venv/bin:$PATH" \
    HOST=0.0.0.0 \
    PORT=3002 \
    PYTHONUNBUFFERED=1

EXPOSE 3002

CMD ["python", "main.py", "server"]
