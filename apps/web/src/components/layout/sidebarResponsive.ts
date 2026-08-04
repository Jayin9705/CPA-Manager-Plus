export const MOBILE_VIEWPORT_QUERY = '(max-width: 768px)';
export const COMPACT_VIEWPORT_QUERY = '(max-width: 1023px)';

interface ResolveSidebarResponsiveStateOptions {
  isMobileViewport: boolean;
  isCompactViewport: boolean;
  preferredSidebarCollapsed: boolean;
  tabletSidebarExpanded: boolean;
  sidebarOpen: boolean;
}

export function resolveSidebarResponsiveState({
  isMobileViewport,
  isCompactViewport,
  preferredSidebarCollapsed,
  tabletSidebarExpanded,
  sidebarOpen,
}: ResolveSidebarResponsiveStateOptions) {
  const isTabletViewport = isCompactViewport && !isMobileViewport;
  const sidebarCollapsed = isTabletViewport
    ? !tabletSidebarExpanded
    : preferredSidebarCollapsed;
  const sidebarExpanded = isMobileViewport ? sidebarOpen : !sidebarCollapsed;

  return {
    isTabletViewport,
    sidebarCollapsed,
    sidebarExpanded,
    showSidebarLabels: sidebarExpanded,
  };
}
