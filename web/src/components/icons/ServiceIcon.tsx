// Service icons rendered as small text badges with stable per-name colors.
// We don't ship brand logos here — too many trademarks, too much weight.
// A label-driven badge is honest about what we know (the workload's image
// matched a keyword) and stays accessible.

const PALETTE: Record<string, { bg: string; text: string }> = {
  // databases / caches
  postgres: { bg: "#1f2c46", text: "#5b9cf0" },
  mariadb: { bg: "#3a2710", text: "#f59e0b" },
  mongodb: { bg: "#1e3324", text: "#10b981" },
  redis: { bg: "#3a1c1c", text: "#ef4444" },
  valkey: { bg: "#1f2540", text: "#a78bfa" },
  dragonfly: { bg: "#1c2540", text: "#818cf8" },
  memcached: { bg: "#1f2c46", text: "#7dd3fc" },
  clickhouse: { bg: "#3a2c10", text: "#fde047" },
  cassandra: { bg: "#1f1f1f", text: "#a1a1aa" },
  cockroachdb: { bg: "#1c2c46", text: "#60a5fa" },
  influxdb: { bg: "#1f2540", text: "#a78bfa" },
  neo4j: { bg: "#1c2c46", text: "#38bdf8" },
  couchdb: { bg: "#3a1c1c", text: "#f87171" },
  surrealdb: { bg: "#3a1c2c", text: "#f472b6" },
  qdrant: { bg: "#3a1c1c", text: "#fb7185" },
  weaviate: { bg: "#1e3324", text: "#34d399" },
  milvus: { bg: "#1f2540", text: "#a78bfa" },
  meilisearch: { bg: "#3a1c2c", text: "#f472b6" },
  typesense: { bg: "#3a1c1c", text: "#fb7185" },
  etcd: { bg: "#1f1f1f", text: "#fafafa" },

  // web / proxy / cdn
  nginx: { bg: "#1a3a2c", text: "#34d399" },
  caddy: { bg: "#1c2c46", text: "#60a5fa" },
  traefik: { bg: "#3a2410", text: "#fbbf24" },
  haproxy: { bg: "#1c2540", text: "#a78bfa" },
  apache: { bg: "#3a1c1c", text: "#f87171" },
  envoy: { bg: "#3a2410", text: "#fb923c" },
  kong: { bg: "#1f2c46", text: "#5b9cf0" },
  openresty: { bg: "#1f2c46", text: "#5b9cf0" },
  varnish: { bg: "#3a2410", text: "#f97316" },
  cloudflared: { bg: "#3a2410", text: "#fb923c" },

  // observability
  grafana: { bg: "#3a2410", text: "#f97316" },
  prometheus: { bg: "#3a2410", text: "#fb923c" },
  alertmanager: { bg: "#3a1c1c", text: "#fb7185" },
  loki: { bg: "#3a2410", text: "#fbbf24" },
  tempo: { bg: "#3a2410", text: "#fdba74" },
  mimir: { bg: "#3a2410", text: "#fbbf24" },
  jaeger: { bg: "#1f2540", text: "#a78bfa" },
  "uptime-kuma": { bg: "#1e3324", text: "#10b981" },
  telegraf: { bg: "#1f2540", text: "#a78bfa" },
  vector: { bg: "#1c2c46", text: "#60a5fa" },
  victoriametrics: { bg: "#3a2410", text: "#f97316" },
  signoz: { bg: "#1f2c46", text: "#5b9cf0" },
  fluentbit: { bg: "#1c2c46", text: "#60a5fa" },
  netdata: { bg: "#1e3324", text: "#10b981" },

  // logging / search
  elasticsearch: { bg: "#3a2c10", text: "#fcd34d" },
  opensearch: { bg: "#1c2c46", text: "#60a5fa" },
  kibana: { bg: "#3a1c2c", text: "#f472b6" },
  graylog: { bg: "#1f2c46", text: "#5b9cf0" },
  dozzle: { bg: "#1f1f1f", text: "#a1a1aa" },

  // messaging
  rabbitmq: { bg: "#3a2410", text: "#fb923c" },
  nats: { bg: "#1f2540", text: "#a78bfa" },
  kafka: { bg: "#1f1f1f", text: "#fafafa" },
  redpanda: { bg: "#3a1c1c", text: "#fb7185" },
  mosquitto: { bg: "#1c2c46", text: "#60a5fa" },

  // identity / auth
  vault: { bg: "#1f1f1f", text: "#fafafa" },
  keycloak: { bg: "#1c2c46", text: "#60a5fa" },
  authelia: { bg: "#3a1c1c", text: "#f87171" },
  authentik: { bg: "#3a1c2c", text: "#f472b6" },
  pocketid: { bg: "#1f2c46", text: "#5b9cf0" },
  lldap: { bg: "#1c2540", text: "#a78bfa" },
  openldap: { bg: "#1c2540", text: "#a78bfa" },
  vaultwarden: { bg: "#1f2c46", text: "#5b9cf0" },

  // devops / code / ci
  gitea: { bg: "#1f2c46", text: "#5b9cf0" },
  forgejo: { bg: "#3a1c1c", text: "#fb7185" },
  gitlab: { bg: "#3a2410", text: "#fb923c" },
  drone: { bg: "#3a1c1c", text: "#fb7185" },
  woodpecker: { bg: "#3a2410", text: "#f97316" },
  jenkins: { bg: "#3a1c1c", text: "#f87171" },
  harbor: { bg: "#1f2c46", text: "#5b9cf0" },
  registry: { bg: "#1f1f1f", text: "#a1a1aa" },
  nexus: { bg: "#1f2c46", text: "#5b9cf0" },
  sonarqube: { bg: "#1c2c46", text: "#60a5fa" },
  argocd: { bg: "#3a2410", text: "#f97316" },
  portainer: { bg: "#1c2c46", text: "#60a5fa" },
  watchtower: { bg: "#1f1f1f", text: "#a1a1aa" },

  // self-hosted apps
  nextcloud: { bg: "#1c2c46", text: "#60a5fa" },
  immich: { bg: "#1f2540", text: "#a78bfa" },
  photoprism: { bg: "#1f2540", text: "#a78bfa" },
  paperless: { bg: "#3a2410", text: "#fbbf24" },
  vikunja: { bg: "#1e3324", text: "#34d399" },
  syncthing: { bg: "#1c2c46", text: "#60a5fa" },
  audiobookshelf: { bg: "#3a2410", text: "#fb923c" },
  bookstack: { bg: "#1e3324", text: "#10b981" },
  miniflux: { bg: "#3a2410", text: "#fb923c" },
  wallabag: { bg: "#3a2410", text: "#f97316" },
  seafile: { bg: "#1c2c46", text: "#60a5fa" },
  freshrss: { bg: "#3a2410", text: "#fb923c" },
  healthvault: { bg: "#3a1c1c", text: "#fb7185" },

  // media / *arr
  jellyfin: { bg: "#3a1c2c", text: "#f472b6" },
  plex: { bg: "#3a2410", text: "#fbbf24" },
  emby: { bg: "#1e3324", text: "#10b981" },
  radarr: { bg: "#3a2410", text: "#fbbf24" },
  sonarr: { bg: "#1c2c46", text: "#60a5fa" },
  lidarr: { bg: "#1e3324", text: "#34d399" },
  bazarr: { bg: "#1f2540", text: "#a78bfa" },
  prowlarr: { bg: "#3a2410", text: "#fb923c" },
  jellyseerr: { bg: "#3a1c2c", text: "#f472b6" },
  sabnzbd: { bg: "#3a2410", text: "#fbbf24" },
  qbittorrent: { bg: "#1c2c46", text: "#60a5fa" },
  transmission: { bg: "#3a1c1c", text: "#fb7185" },
  navidrome: { bg: "#1f2540", text: "#a78bfa" },

  // smart home
  homeassistant: { bg: "#1c2c46", text: "#60a5fa" },
  zigbee2mqtt: { bg: "#3a1c2c", text: "#f472b6" },
  nodered: { bg: "#3a1c1c", text: "#f87171" },
  frigate: { bg: "#1f2540", text: "#a78bfa" },
  esphome: { bg: "#1e3324", text: "#34d399" },

  // networking / dns / vpn
  pihole: { bg: "#3a1c1c", text: "#fb7185" },
  adguard: { bg: "#1e3324", text: "#10b981" },
  unbound: { bg: "#1f1f1f", text: "#a1a1aa" },
  headscale: { bg: "#1f1f1f", text: "#fafafa" },
  tailscale: { bg: "#1f1f1f", text: "#fafafa" },
  wireguard: { bg: "#3a1c1c", text: "#fb7185" },
  netbird: { bg: "#1c2c46", text: "#60a5fa" },

  // mail / push
  mailcow: { bg: "#1f2c46", text: "#5b9cf0" },
  postfix: { bg: "#1f1f1f", text: "#a1a1aa" },
  dovecot: { bg: "#1f2c46", text: "#5b9cf0" },
  ntfy: { bg: "#1e3324", text: "#34d399" },
  gotify: { bg: "#1c2c46", text: "#60a5fa" },

  // security
  crowdsec: { bg: "#3a1c1c", text: "#fb7185" },
  anubis: { bg: "#1f1f1f", text: "#fafafa" },

  // self
  mon: { bg: "#1e3324", text: "#10b981" },
};

export function ServiceBadge({ name }: { name: string }) {
  const p = PALETTE[name] ?? { bg: "#27272a", text: "#a1a1aa" };
  return (
    <span
      title={name}
      className="inline-flex items-center rounded-md px-1.5 py-0.5 font-mono text-[10px] font-medium"
      style={{ backgroundColor: p.bg, color: p.text }}
    >
      {name}
    </span>
  );
}

export function ServiceBadges({ services, max = 3 }: { services?: string[]; max?: number }) {
  if (!services || services.length === 0) return null;
  const shown = services.slice(0, max);
  const overflow = services.length - shown.length;
  return (
    <div className="flex flex-wrap items-center gap-1">
      {shown.map((s) => (
        <ServiceBadge key={s} name={s} />
      ))}
      {overflow > 0 && (
        <span
          className="cursor-help rounded-md bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] font-medium text-fg-muted"
          title={services.join(", ")}
        >
          +{overflow}
        </span>
      )}
    </div>
  );
}
