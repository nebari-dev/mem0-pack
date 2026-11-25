# Server image: mem0 v2.2.1's REST server plus the pack's wrapper (mem0pack).
# Build with `docker build -t mem0-pack-server:dev image/`.
ARG PYTHON_IMAGE=python:3.12-slim@sha256:191083cdf6c7bda8c76158983163ead5d5cf7b77cf3033e805bb0f5296f51b4b

FROM ${PYTHON_IMAGE} AS fetch
ARG MEM0_COMMIT=94c3fe9f238f3dbf29c9ce98643bd71eb13077cd
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN git clone https://github.com/mem0ai/mem0 /src \
 && cd /src \
 && git checkout "${MEM0_COMMIT}" \
 && test "$(git rev-parse HEAD)" = "${MEM0_COMMIT}" \
 && mkdir -p /out/server \
 && cp server/*.py server/alembic.ini /out/server/ \
 && cp -r server/routers server/alembic /out/server/ \
 && echo "${MEM0_COMMIT}" > /out/server/.mem0-commit

FROM ${PYTHON_IMAGE}
ARG MEM0_COMMIT=94c3fe9f238f3dbf29c9ce98643bd71eb13077cd
LABEL org.opencontainers.image.source="https://github.com/nebari-dev/mem0-pack" \
      io.nebari.mem0.commit="${MEM0_COMMIT}" \
      io.nebari.mem0.version="2.2.1"

COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --require-hashes --no-deps -r /tmp/requirements.txt \
 && rm /tmp/requirements.txt

RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin mem0 \
 && mkdir -p /var/lib/mem0 && chown 10001:10001 /var/lib/mem0

COPY --from=fetch /out/server /app/server
COPY mem0pack /app/mem0pack

ENV MEM0_TELEMETRY=false \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    MEM0_DIR=/var/lib/mem0/home \
    HISTORY_DB_PATH=/var/lib/mem0/history/history.db \
    MEM0_TELEMETRY_STATE_PATH=/var/lib/mem0/history/telemetry.json

USER 10001
WORKDIR /app
EXPOSE 8000
ENTRYPOINT ["python", "-m", "mem0pack.serve"]
