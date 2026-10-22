export function LogoMark({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <defs>
        <linearGradient id="gb-core" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" stopColor="#ff9500" />
          <stop offset="0.52" stopColor="#ff3b30" />
          <stop offset="1" stopColor="#ff2d55" />
        </linearGradient>
      </defs>
      <circle cx="16" cy="16" r="14.5" fill="none" stroke="url(#gb-core)" strokeOpacity="0.28" strokeWidth="1.5" />
      <circle cx="16" cy="16" r="9.5" fill="none" stroke="url(#gb-core)" strokeOpacity="0.6" strokeWidth="1.5" />
      <circle cx="16" cy="16" r="4.8" fill="url(#gb-core)" />
    </svg>
  );
}
