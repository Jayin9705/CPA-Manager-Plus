const JAY_AI_FAVICON_SVG = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128" role="img" aria-label="J">
  <defs>
    <linearGradient id="bg" x1="12" y1="8" x2="116" y2="120" gradientUnits="userSpaceOnUse">
      <stop stop-color="#07131f" />
      <stop offset="1" stop-color="#101d3d" />
    </linearGradient>
    <linearGradient id="j" x1="24" y1="20" x2="92" y2="108" gradientUnits="userSpaceOnUse">
      <stop stop-color="#47e7ff" />
      <stop offset="0.52" stop-color="#20b9ed" />
      <stop offset="1" stop-color="#6574ff" />
    </linearGradient>
    <filter id="glow" x="-50%" y="-50%" width="200%" height="200%">
      <feGaussianBlur stdDeviation="4" result="blur" />
      <feMerge>
        <feMergeNode in="blur" />
        <feMergeNode in="SourceGraphic" />
      </feMerge>
    </filter>
  </defs>
  <rect width="128" height="128" rx="28" fill="url(#bg)" />
  <rect x="5" y="5" width="118" height="118" rx="24" fill="none" stroke="#43d9f5" stroke-opacity=".25" />
  <path
    d="M38 19h54v12H80v46c0 18-11 29-29 29-18 0-29-10-29-27h14c0 10 5 15 15 15 10 0 15-6 15-17V31H38V19Z"
    fill="#35d4f3"
    fill-opacity=".28"
    filter="url(#glow)"
  />
  <path
    d="M38 19h54v12H80v46c0 18-11 29-29 29-18 0-29-10-29-27h14c0 10 5 15 15 15 10 0 15-6 15-17V31H38V19Z"
    fill="url(#j)"
  />
  <path d="M88 22h8" stroke="#c3f8ff" stroke-linecap="round" stroke-width="3" />
</svg>`;

export const JAY_AI_FAVICON = `data:image/svg+xml,${encodeURIComponent(JAY_AI_FAVICON_SVG)}`;
