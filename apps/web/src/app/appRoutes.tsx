import { lazy, Suspense, type ReactElement } from 'react';
import { Navigate, type RouteObject } from 'react-router-dom';
import { RootShell } from './RootShell';

const LazyAuthenticatedApp = lazy(() =>
  import('./AuthenticatedApp').then(({ AuthenticatedApp }) => ({ default: AuthenticatedApp }))
);
const LazyDemoPage = lazy(() =>
  import('@/pages/DemoPage').then(({ DemoPage }) => ({ default: DemoPage }))
);
const LazyLoginPage = lazy(() =>
  import('@/features/login/LoginPage').then(({ LoginPage }) => ({ default: LoginPage }))
);

const lazyElement = (element: ReactElement) => (
  <Suspense fallback={<div className="route-loading" role="status" aria-label="Loading" />}>
    {element}
  </Suspense>
);

const appRoutes: RouteObject[] = [
  {
    element: <RootShell />,
    children: __DEMO_SITE__
      ? [
          { index: true, element: <Navigate to="/demo" replace /> },
          { path: '/demo/*', element: lazyElement(<LazyDemoPage />) },
          { path: '*', element: <Navigate to="/demo" replace /> },
        ]
      : [
          { path: '/login', element: lazyElement(<LazyLoginPage />) },
          { path: '/*', element: lazyElement(<LazyAuthenticatedApp />) },
        ],
  },
];

export const createAppRoutes = (): RouteObject[] => appRoutes;
