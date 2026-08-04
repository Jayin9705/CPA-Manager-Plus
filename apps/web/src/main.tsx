import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@/styles/global.scss';
import { JAY_AI_FAVICON } from '@/assets/jayAiFaviconInline';
import { loadRuntimeConfig, RuntimeConfigError } from '@/config/runtimeConfig';
import App from './App.tsx';

document.title = 'JAY AI';
document.documentElement.setAttribute('translate', 'no');
document.documentElement.classList.add('notranslate');
// Keep the new console skin isolated from the embedded/legacy manager page.
// The build flag is compile-time, so the old `/management.html` bundle never
// receives the separated-v2 selectors below.
document.documentElement.dataset.console = __SEPARATED_SITE__ ? 'separated-v2' : 'embedded';

const faviconEl = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
if (faviconEl) {
  faviconEl.href = JAY_AI_FAVICON;
  faviconEl.type = 'image/svg+xml';
} else {
  const newFavicon = document.createElement('link');
  newFavicon.rel = 'icon';
  newFavicon.type = 'image/svg+xml';
  newFavicon.href = JAY_AI_FAVICON;
  document.head.appendChild(newFavicon);
}

const root = createRoot(document.getElementById('root')!);

const renderRuntimeConfigError = (error: unknown) => {
  const message =
    error instanceof RuntimeConfigError ? error.message : 'Unable to load deployment configuration';
  root.render(
    <main className="runtime-config-error" aria-labelledby="runtime-config-error-title">
      <div className="runtime-config-error__card">
        <p className="runtime-config-error__eyebrow">CPA Manager Plus</p>
        <h1 id="runtime-config-error-title">Deployment configuration error</h1>
        <p>{message}</p>
        <p className="runtime-config-error__hint">
          Check runtime-config.json and reload this page.
        </p>
      </div>
    </main>
  );
};

void loadRuntimeConfig()
  .then(() => {
    root.render(
      <StrictMode>
        <App />
      </StrictMode>
    );
  })
  .catch(renderRuntimeConfigError);
