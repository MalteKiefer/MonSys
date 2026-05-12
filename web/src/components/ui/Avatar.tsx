// Circular user avatar. Renders the uploaded image when `hasAvatar` is true,
// otherwise falls back to the first letter of the email on an accent
// background. The cache-bust query param uses `updatedAt` so the browser
// picks up the new image immediately after an upload without forcing
// no-store on the response.

type AvatarSize = "sm" | "md" | "lg";

const SIZE_PX: Record<AvatarSize, number> = {
  sm: 32,
  md: 40,
  lg: 96,
};

const TEXT_CLS: Record<AvatarSize, string> = {
  sm: "text-[11px]",
  md: "text-sm",
  lg: "text-2xl",
};

export function Avatar({
  userId,
  hasAvatar,
  updatedAt,
  email,
  size = "md",
  className = "",
}: {
  userId: string;
  hasAvatar?: boolean | null;
  updatedAt?: string | null;
  email: string;
  size?: AvatarSize;
  className?: string;
}) {
  const px = SIZE_PX[size];
  const dim = { width: px, height: px };
  const initial = (email?.[0] ?? "?").toUpperCase();

  if (hasAvatar) {
    // Cache-bust via `?v=` so a freshly uploaded image actually shows up.
    // Encode the timestamp to keep stray spaces / colons safe in URLs.
    const v = updatedAt ? encodeURIComponent(updatedAt) : "1";
    const src = `/v1/users/${userId}/avatar?v=${v}`;
    return (
      <img
        src={src}
        alt={email}
        width={px}
        height={px}
        style={dim}
        className={`shrink-0 rounded-full object-cover ring-1 ring-inset ring-border ${className}`}
      />
    );
  }

  return (
    <span
      aria-label={email}
      title={email}
      style={dim}
      className={`inline-flex shrink-0 items-center justify-center rounded-full bg-accent/20 font-semibold uppercase text-accent ring-1 ring-inset ring-accent/30 ${TEXT_CLS[size]} ${className}`}
    >
      {initial}
    </span>
  );
}
