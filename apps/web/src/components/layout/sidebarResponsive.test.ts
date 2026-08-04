import { describe, expect, it } from 'vitest';
import { resolveSidebarResponsiveState } from './sidebarResponsive';

describe('resolveSidebarResponsiveState', () => {
  it('uses a closed drawer on mobile without changing the saved desktop preference', () => {
    expect(
      resolveSidebarResponsiveState({
        isMobileViewport: true,
        isCompactViewport: true,
        preferredSidebarCollapsed: false,
        tabletSidebarExpanded: false,
        sidebarOpen: false,
      })
    ).toEqual({
      isTabletViewport: false,
      sidebarCollapsed: false,
      sidebarExpanded: false,
      showSidebarLabels: false,
    });
  });

  it('automatically collapses the persistent sidebar on tablet widths', () => {
    expect(
      resolveSidebarResponsiveState({
        isMobileViewport: false,
        isCompactViewport: true,
        preferredSidebarCollapsed: false,
        tabletSidebarExpanded: false,
        sidebarOpen: false,
      })
    ).toMatchObject({
      isTabletViewport: true,
      sidebarCollapsed: true,
      sidebarExpanded: false,
      showSidebarLabels: false,
    });
  });

  it('allows a temporary tablet expansion without overwriting desktop state', () => {
    expect(
      resolveSidebarResponsiveState({
        isMobileViewport: false,
        isCompactViewport: true,
        preferredSidebarCollapsed: true,
        tabletSidebarExpanded: true,
        sidebarOpen: false,
      })
    ).toMatchObject({
      isTabletViewport: true,
      sidebarCollapsed: false,
      sidebarExpanded: true,
      showSidebarLabels: true,
    });
  });

  it('restores the saved preference on desktop widths', () => {
    expect(
      resolveSidebarResponsiveState({
        isMobileViewport: false,
        isCompactViewport: false,
        preferredSidebarCollapsed: true,
        tabletSidebarExpanded: true,
        sidebarOpen: true,
      })
    ).toMatchObject({
      isTabletViewport: false,
      sidebarCollapsed: true,
      sidebarExpanded: false,
      showSidebarLabels: false,
    });
  });
});
