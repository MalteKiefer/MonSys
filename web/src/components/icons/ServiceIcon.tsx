// Service icons rendered as small text badges with stable per-name colors.
// We don't ship brand logos here — too many trademarks, too much weight.
// A label-driven badge is honest about what we know (the workload's image
// matched a keyword) and stays accessible.

const PALETTE: Record<string, { bg: string; text: string }> = {
  postgres: { bg: "#1f2c46", text: "#5b9cf0" },
  mariadb: { bg: "#3a2710", text: "#f59e0b" },
  mongodb: { bg: "#1e3324", text: "#10b981" },
  redis: { bg: "#3a1c1c", text: "#ef4444" },
  valkey: { bg: "#1f2540", text: "#a78bfa" },
  nginx: { bg: "#1a3a2c", text: "#34d399" },
  caddy: { bg: "#1c2c46", text: "#60a5fa" },
  traefik: { bg: "#3a2410", text: "#fbbf24" },
  haproxy: { bg: "#1c2540", text: "#a78bfa" },
  grafana: { bg: "#3a2410", text: "#f97316" },
  prometheus: { bg: "#3a2410", text: "#fb923c" },
  loki: { bg: "#3a2410", text: "#fbbf24" },
  elasticsearch: { bg: "#3a2c10", text: "#fcd34d" },
  opensearch: { bg: "#1c2c46", text: "#60a5fa" },
  rabbitmq: { bg: "#3a2410", text: "#fb923c" },
  nats: { bg: "#1f2540", text: "#a78bfa" },
  kafka: { bg: "#1f1f1f", text: "#fafafa" },
  vault: { bg: "#1f1f1f", text: "#fafafa" },
  keycloak: { bg: "#1c2c46", text: "#60a5fa" },
  gitea: { bg: "#1f2c46", text: "#5b9cf0" },
  forgejo: { bg: "#3a1c1c", text: "#fb7185" },
  gitlab: { bg: "#3a2410", text: "#fb923c" },
  nextcloud: { bg: "#1c2c46", text: "#60a5fa" },
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

export function ServiceBadges({ services, max = 6 }: { services?: string[]; max?: number }) {
  if (!services || services.length === 0) return null;
  const shown = services.slice(0, max);
  const overflow = services.length - shown.length;
  return (
    <div className="flex flex-wrap items-center gap-1">
      {shown.map((s) => (
        <ServiceBadge key={s} name={s} />
      ))}
      {overflow > 0 && (
        <span className="text-[10px] text-fg-subtle" title={services.slice(max).join(", ")}>
          +{overflow}
        </span>
      )}
    </div>
  );
}
