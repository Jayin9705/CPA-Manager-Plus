import { lazy, Suspense, useEffect, useState } from 'react';

const LazyFullApp = lazy(() =>
  import('@/app/FullApp').then(({ FullApp }) => ({ default: FullApp }))
);
const LazyLoginApp = lazy(() =>
  import('@/app/LoginApp').then(({ LoginApp }) => ({ default: LoginApp }))
);

const getHashPath = () => {
  if (typeof window === 'undefined') return '/';
  const hash = window.location.hash.replace(/^#/, '');
  if (hash) return hash.split('?')[0] || '/';
  if (__SEPARATED_SITE__ && window.location.pathname !== '/') {
    return window.location.pathname;
  }
  return '/';
};

const isLoginEntryPath = (path: string) => path === '/' || path === '/login';

function App() {
  const [hashPath, setHashPath] = useState(getHashPath);

  useEffect(() => {
    const handleHashChange = () => setHashPath(getHashPath());
    window.addEventListener('hashchange', handleHashChange);
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, []);

  const app = __DEMO_SITE__ || !isLoginEntryPath(hashPath) ? <LazyFullApp /> : <LazyLoginApp />;

  return (
    <Suspense fallback={<div className="route-loading" role="status" aria-label="Loading" />}>
      {app}
    </Suspense>
  );
}

export default App;
