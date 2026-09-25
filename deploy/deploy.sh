#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="deploy/docker-compose.yml"
cd "$ROOT_DIR"

if [[ ! -f go.mod || ! -f web/package.json || ! -f "$COMPOSE_FILE" ]]; then
  printf 'Run this script from a Centilog clone containing go.mod, web/, and %s.\n' "$COMPOSE_FILE" >&2
  exit 1
fi

if [[ $EUID -eq 0 ]]; then
  SUDO=()
elif command -v sudo >/dev/null 2>&1; then
  SUDO=(sudo)
else
  printf 'This script needs root or sudo to install Docker and start its service.\n' >&2
  exit 1
fi

install_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    "${SUDO[@]}" systemctl enable --now docker
    return
  fi

  if [[ ! -r /etc/os-release ]]; then
    printf 'Cannot identify this Linux distribution. This installer supports Debian and Ubuntu.\n' >&2
    exit 1
  fi
  . /etc/os-release
  if [[ "${ID:-}" != "debian" && "${ID:-}" != "ubuntu" ]]; then
    printf 'Unsupported distribution: %s. This installer supports Debian and Ubuntu.\n' "${ID:-unknown}" >&2
    exit 1
  fi
  if [[ -z "${VERSION_CODENAME:-}" ]]; then
    printf 'Could not determine the OS release codename.\n' >&2
    exit 1
  fi

  "${SUDO[@]}" apt-get update
  "${SUDO[@]}" apt-get install -y ca-certificates curl openssl
  "${SUDO[@]}" install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${ID}/gpg" | "${SUDO[@]}" tee /etc/apt/keyrings/docker.asc >/dev/null
  "${SUDO[@]}" chmod a+r /etc/apt/keyrings/docker.asc
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
    "$(dpkg --print-architecture)" "$ID" "$VERSION_CODENAME" \
    | "${SUDO[@]}" tee /etc/apt/sources.list.d/docker.list >/dev/null
  "${SUDO[@]}" apt-get update
  "${SUDO[@]}" apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  "${SUDO[@]}" systemctl enable --now docker
}

install_docker

DOCKER=(docker)
if ! "${DOCKER[@]}" info >/dev/null 2>&1; then
  if ((${#SUDO[@]})); then
    DOCKER=("${SUDO[@]}" docker)
  fi
  if ! "${DOCKER[@]}" info >/dev/null 2>&1; then
    printf 'Docker is installed but cannot be accessed by this user. Use a sudo-enabled account.\n' >&2
    exit 1
  fi
fi
COMPOSE=("${DOCKER[@]}" compose -f "$COMPOSE_FILE")
"${COMPOSE[@]}" version >/dev/null
if ! command -v openssl >/dev/null 2>&1; then
  "${SUDO[@]}" apt-get install -y openssl
fi

create_env_file() {
  umask 077
  cat > .env <<EOF
POSTGRES_DB=centilog
POSTGRES_USER=centilog
POSTGRES_PASSWORD=$(openssl rand -hex 32)
REDIS_PASSWORD=$(openssl rand -hex 32)
CLICKHOUSE_DB=centilog
CLICKHOUSE_USER=centilog
CLICKHOUSE_PASSWORD=$(openssl rand -hex 32)
JWT_SECRET=$(openssl rand -hex 32)
DEMO_SERVICE_API_KEY=$(openssl rand -hex 32)
EOF
  chmod 600 .env
}

if [[ ! -f .env ]]; then
  create_env_file
  printf 'Created .env with random local deployment credentials (file mode 600).\n'
else
  for key in POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD REDIS_PASSWORD CLICKHOUSE_DB CLICKHOUSE_USER CLICKHOUSE_PASSWORD JWT_SECRET DEMO_SERVICE_API_KEY; do
    if ! grep -q "^${key}=" .env; then
      printf '.env is missing %s. Add it or move the existing .env aside and rerun.\n' "$key" >&2
      exit 1
    fi
  done
  chmod 600 .env
fi

install -d -m 0755 sample-logs
touch sample-logs/app.log sample-logs/legacy.log
install -d -m 0700 data/agent-buffer
"${SUDO[@]}" chown -R 10001:10001 data/agent-buffer

read -r -p 'Administrator email [admin@centilog.local]: ' ADMIN_EMAIL
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@centilog.local}"
read -r -s -p 'Administrator password (8-72 characters): ' ADMIN_PASSWORD
printf '\n'
trap 'unset ADMIN_PASSWORD' EXIT
if (( ${#ADMIN_PASSWORD} < 8 || ${#ADMIN_PASSWORD} > 72 )); then
  printf 'Password must be between 8 and 72 characters.\n' >&2
  exit 1
fi

"${COMPOSE[@]}" up --detach --build --wait --wait-timeout 300

demo_api_key="$(sed -n 's/^DEMO_SERVICE_API_KEY=//p' .env | head -n 1)"
"${COMPOSE[@]}" exec -T --env "DEPLOY_DEMO_API_KEY=$demo_api_key" postgres sh -c \
  'psql -v ON_ERROR_STOP=1 -v demo_api_key="$DEPLOY_DEMO_API_KEY" -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null <<'SQL'
UPDATE services SET api_key = :'demo_api_key' WHERE name = 'demo-service';
SQL
"${COMPOSE[@]}" restart ingest agent >/dev/null

admin_exists="$("${COMPOSE[@]}" exec -T --env "DEPLOY_ADMIN_EMAIL=$ADMIN_EMAIL" postgres sh -c \
  'psql -v ON_ERROR_STOP=1 -v admin_email="$DEPLOY_ADMIN_EMAIL" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atq' <<'SQL'
SELECT EXISTS (SELECT 1 FROM users WHERE email = :'admin_email');
SQL
)"
if [[ "$admin_exists" == "t" ]]; then
  printf 'Admin %s already exists; left its password unchanged.\n' "$ADMIN_EMAIL"
else
  printf '%s' "$ADMIN_PASSWORD" | "${COMPOSE[@]}" run --rm --no-deps -T api create-admin "$ADMIN_EMAIL" -
fi
unset ADMIN_PASSWORD
trap - EXIT

printf '\nCentilog deployment is ready.\n'
printf 'Dashboard: http://<VPS-IP>:3000\n'
printf 'Admin email: %s\n' "$ADMIN_EMAIL"
printf 'The demo service API key is stored in the VPS .env file.\n'
printf 'Open TCP port 3000 in your VPS firewall. Put HTTPS in front of the dashboard before using it with real credentials.\n'
printf 'The Redis-to-ClickHouse Processor is not implemented yet; accepted logs currently remain in Redis.\n'