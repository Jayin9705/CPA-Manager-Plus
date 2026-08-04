import { AppLifecycle } from './AppLifecycle';
import { AppRouter } from './AppRouter';

export function FullApp() {
  return (
    <>
      <AppLifecycle />
      <AppRouter />
    </>
  );
}
