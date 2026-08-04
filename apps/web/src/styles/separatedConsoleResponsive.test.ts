// @ts-expect-error -- Vitest runs this contract test in Node; app typings intentionally omit Node.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const separatedConsoleSource = readFileSync(
  new URL('./separated-console.scss', import.meta.url),
  'utf8'
);

describe('separated console responsive shell', () => {
  it('does not reserve desktop sidebar space inside the mobile drawer breakpoint', () => {
    const mobileBreakpointStart = separatedConsoleSource.search(
      /@media\s*\(max-width:\s*768px\)/
    );
    const reducedMotionOffset = separatedConsoleSource
      .slice(mobileBreakpointStart)
      .search(/@media\s*\(prefers-reduced-motion:\s*reduce\)/);
    const reducedMotionStart = mobileBreakpointStart + reducedMotionOffset;
    const mobileRules = separatedConsoleSource.slice(mobileBreakpointStart, reducedMotionStart);

    expect(mobileBreakpointStart).toBeGreaterThanOrEqual(0);
    expect(reducedMotionOffset).toBeGreaterThan(0);
    expect(mobileRules).toMatch(
      /html\[data-console=(?:['"]?separated-v2['"]?)\]\s+\.app-shell\s*{\s*--sidebar-active-width:\s*0px;/
    );
    expect(mobileRules).toMatch(
      /html\[data-console=(?:['"]?separated-v2['"]?)\]\s+\.content\s*{[^}]*width:\s*100%;[^}]*margin-left:\s*0;/s
    );
  });
});
