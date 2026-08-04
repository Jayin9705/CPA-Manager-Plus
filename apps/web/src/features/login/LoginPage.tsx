import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import {
  IconCheck,
  IconEye,
  IconEyeOff,
  IconInfo,
  IconKey,
  IconLanguages,
  IconMoon,
  IconShield,
  IconSun,
  IconTimer,
} from '@/components/ui/icons';
import { useThemeStore } from '@/stores/useThemeStore';
import {
  LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEY,
  USAGE_SERVICE_LAST_CPA_BASE_KEY,
  getUsageServiceErrorCode,
  loginServiceApi,
} from './loginApi';
import {
  detectApiBaseFromLocation,
  normalizeApiBase,
  resolveDefaultCPAConnectionBase,
} from '@/utils/connection';
import { LANGUAGE_LABEL_KEYS, LANGUAGE_ORDER } from '@/utils/constants';
import { isSupportedLanguage } from '@/utils/language';
import type { ApiError } from '@/types';
import { isSeparatedSite } from '@/config/runtimeConfig';
import { resolveUsageServiceLoginMode } from './loginMode';
import { loadAuthStore, readStoredLoginState } from './loginRuntime';
import { useLoginLanguage } from './useLoginLanguage';
import styles from './LoginPage.module.scss';

type UsageSetupStep = 'admin' | 'connection' | 'cpaKey' | 'monitoring' | 'polling' | 'review';
const CONFIG_TAB_STORAGE_KEY = 'config-management:tab';
const LOGIN_REDIRECT_STORAGE_KEY = 'config-management:login-redirect';

const readLoginRedirect = () => {
  try {
    const redirect = sessionStorage.getItem(LOGIN_REDIRECT_STORAGE_KEY);
    sessionStorage.removeItem(LOGIN_REDIRECT_STORAGE_KEY);
    return redirect?.startsWith('/') && redirect !== '/' ? redirect : '/dashboard';
  } catch {
    return '/dashboard';
  }
};

const navigateFromLogin = (path: string) => {
  if (typeof window === 'undefined') return;
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  const nextHash = `#${normalizedPath}`;

  // HashRouter listens for the browser's native hashchange event. Updating the
  // URL with history.replaceState requires a synthetic event and can leave the
  // lightweight login shell mounted while the address already points elsewhere.
  if (window.location.hash === nextHash) {
    window.dispatchEvent(new Event('hashchange'));
    return;
  }

  window.location.replace(
    `${window.location.pathname}${window.location.search}${nextHash}`
  );
};

function getLocalizedErrorMessage(
  error: unknown,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  const usageServiceCode = getUsageServiceErrorCode(error);
  if (usageServiceCode) {
    return t(`usage_service_errors.${usageServiceCode}`, {
      defaultValue: t('usage_service_errors.request_failed'),
    });
  }

  const apiError = error as Partial<ApiError>;
  const status = typeof apiError.status === 'number' ? apiError.status : undefined;
  const code = typeof apiError.code === 'string' ? apiError.code : undefined;
  const message =
    error instanceof Error
      ? error.message
      : typeof apiError.message === 'string'
        ? apiError.message
        : typeof error === 'string'
          ? error
          : '';

  const withHttpStatus = (summary: string) => {
    if (!status) return summary;

    const genericAxiosMessage = `Request failed with status code ${status}`;
    const detail = message.trim();
    const backendDetail =
      detail && detail !== genericAxiosMessage
        ? ` (${t('login.error_backend_detail')}: ${detail})`
        : '';

    return `HTTP ${status}: ${summary}${backendDetail}`;
  };

  if (status === 401) return withHttpStatus(t('login.error_unauthorized'));
  if (status === 403) return withHttpStatus(t('login.error_forbidden'));
  if (status === 404) return withHttpStatus(t('login.error_not_found'));
  if (status && status >= 500) return withHttpStatus(t('login.error_server'));
  if (code === 'ECONNABORTED' || message.toLowerCase().includes('timeout')) {
    return t('login.error_timeout');
  }
  if (code === 'ERR_NETWORK' || message.toLowerCase().includes('network error')) {
    return t('login.error_network');
  }
  if (code === 'ERR_CERT_AUTHORITY_INVALID' || message.toLowerCase().includes('certificate')) {
    return t('login.error_ssl');
  }
  if (message.toLowerCase().includes('cors') || message.toLowerCase().includes('cross-origin')) {
    return t('login.error_cors');
  }

  return withHttpStatus(t('login.error_invalid'));
}

export function LoginPage() {
  const { language, setLanguage, t } = useLoginLanguage();
  const theme = useThemeStore((state) => state.theme);
  const cycleTheme = useThemeStore((state) => state.cycleTheme);
  const languageMenuRef = useRef<HTMLDivElement | null>(null);

  const detectedBase = useMemo(() => detectApiBaseFromLocation(), []);
  const storedLoginState = useMemo(readStoredLoginState, []);
  const storedBase = storedLoginState.apiBase;
  const storedKey = storedLoginState.managementKey;
  const storedRememberPassword = storedLoginState.rememberPassword;
  const [apiBase, setApiBase] = useState(() => storedBase || detectedBase);
  const [adminEmail, setAdminEmail] = useState('');
  const [adminPassword, setAdminPassword] = useState('');
  const [cpaManagementKey, setCPAManagementKey] = useState(() => storedKey || '');
  const [showCustomBase, setShowCustomBase] = useState(false);
  const [showAdminPassword, setShowAdminPassword] = useState(false);
  const [showCPAManagementKey, setShowCPAManagementKey] = useState(false);
  const [rememberCredential, setRememberCredential] = useState(
    () => storedRememberPassword || Boolean(storedKey)
  );
  const [requestMonitoringEnabled, setRequestMonitoringEnabled] = useState(true);
  const [pollIntervalMs, setPollIntervalMs] = useState('500');
  const [loading, setLoading] = useState(false);
  const [autoLoginSuccess, setAutoLoginSuccess] = useState(false);
  const [error, setError] = useState('');
  // The separated console is always backed by Manager Server. Start in that
  // mode so a slow/temporarily unavailable info probe cannot render the
  // unrelated CPA management-key form.
  const [hostedByUsageService, setHostedByUsageService] = useState(() => isSeparatedSite());
  const [usageServiceNeedsSetup, setUsageServiceNeedsSetup] = useState(false);
  const [languageMenuOpen, setLanguageMenuOpen] = useState(false);
  const [usageSetupStep, setUsageSetupStep] = useState<UsageSetupStep>('admin');
  const loginRedirect = useMemo(() => readLoginRedirect(), []);

  const isManagerServerMode = hostedByUsageService;

  const usageSetupSteps = useMemo<UsageSetupStep[]>(
    () => [
      'admin',
      'connection',
      'cpaKey',
      'monitoring',
      ...(requestMonitoringEnabled ? (['polling'] as UsageSetupStep[]) : []),
      'review',
    ],
    [requestMonitoringEnabled]
  );
  const usageSetupStepIndex = Math.max(0, usageSetupSteps.indexOf(usageSetupStep));
  const usageSetupIsFirstStep = usageSetupStepIndex <= 0;
  const usageSetupIsLastStep = usageSetupStep === 'review';
  const usageSetupStepLabels = useMemo<Record<UsageSetupStep, string>>(
    () => ({
      admin: t('login.step_admin_account'),
      connection: t('login.step_connection'),
      cpaKey: t('login.step_cpa_key'),
      monitoring: t('login.step_monitoring'),
      polling: t('login.step_polling'),
      review: t('login.step_review'),
    }),
    [t]
  );
  const toggleLanguageMenu = useCallback(() => {
    setLanguageMenuOpen((prev) => !prev);
  }, []);

  const handleLanguageSelect = useCallback(
    (selectedLanguage: string) => {
      if (!isSupportedLanguage(selectedLanguage)) {
        return;
      }

      setLanguage(selectedLanguage);
      setLanguageMenuOpen(false);
    },
    [setLanguage]
  );

  useEffect(() => {
    if (!languageMenuOpen) {
      return;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (!languageMenuRef.current?.contains(event.target as Node)) {
        setLanguageMenuOpen(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setLanguageMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleEscape);
    };
  }, [languageMenuOpen]);

  useEffect(() => {
    const init = async () => {
      let detectedUsageService = false;
      let detectedUsageServiceConfigured = false;
      try {
        const info = await loginServiceApi.getInfo(detectedBase, 5000);
        const mode = resolveUsageServiceLoginMode(info);
        detectedUsageService = mode.hostedByUsageService;
        detectedUsageServiceConfigured = detectedUsageService && !mode.usageServiceNeedsSetup;
        setHostedByUsageService(mode.hostedByUsageService);
        setUsageServiceNeedsSetup(mode.usageServiceNeedsSetup);
      } catch {
        detectedUsageService = false;
        detectedUsageServiceConfigured = false;
        setHostedByUsageService(false);
        setUsageServiceNeedsSetup(false);
      }

      // Do not pull the full authenticated application into the first paint.
      // A manager session must still be checked, while a fresh external-panel
      // login can remain completely local until the user submits the form.
      const hasStoredExternalSession = Boolean(storedKey);
      if (detectedUsageService || hasStoredExternalSession) {
        const authStore = await loadAuthStore();
        const autoLoggedIn = await authStore.getState().restoreSession({
          expectedMode: detectedUsageService ? 'manager_embedded' : 'external_panel',
          expectedPanelBase: detectedUsageService ? detectedBase : undefined,
        });
        if (autoLoggedIn) {
          setAutoLoginSuccess(true);
          const redirect =
            autoLoggedIn.recoveryMode === 'manager_config' ? '/config' : loginRedirect;
          if (autoLoggedIn.recoveryMode === 'manager_config') {
            localStorage.setItem(CONFIG_TAB_STORAGE_KEY, 'manager');
          }
          navigateFromLogin(redirect);
          return;
        }
      }

      const lastCPAForUsageService =
        localStorage.getItem(USAGE_SERVICE_LAST_CPA_BASE_KEY) ||
        localStorage.getItem(LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEY) ||
        '';
      const defaultCPAConnectionBase = resolveDefaultCPAConnectionBase({
        hostedByUsageService: detectedUsageService,
        currentBase: detectedBase,
      });
      setApiBase(
        detectedUsageService
          ? detectedUsageServiceConfigured
            ? detectedBase
            : lastCPAForUsageService || defaultCPAConnectionBase
          : storedBase || detectedBase
      );
      setShowCustomBase(detectedUsageService && !detectedUsageServiceConfigured);
      if (detectedUsageService) {
        setCPAManagementKey('');
      } else {
        setCPAManagementKey(storedKey || '');
      }
      setRememberCredential(
        storedRememberPassword || (!detectedUsageService && Boolean(storedKey))
      );
    };

    init();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!usageSetupSteps.includes(usageSetupStep)) {
      setUsageSetupStep('review');
    }
  }, [usageSetupStep, usageSetupSteps]);

  const validateUsageSetupStep = useCallback(
    (step: UsageSetupStep) => {
      if (step === 'admin') {
        if (!adminEmail.trim()) {
          setError(t('login.email_required'));
          return false;
        }
        if (!adminPassword) {
          setError(t('login.password_required'));
          return false;
        }
      }
      if (step === 'connection' && !apiBase.trim()) {
        setError(t('login.cpa_address_required'));
        return false;
      }
      if (step === 'cpaKey' && !cpaManagementKey.trim()) {
        setError(t('login.cpa_management_key_required'));
        return false;
      }
      if (step === 'polling') {
        const parsedPollIntervalMs = Number(pollIntervalMs);
        if (
          !/^\d+$/.test(pollIntervalMs.trim()) ||
          !Number.isFinite(parsedPollIntervalMs) ||
          parsedPollIntervalMs <= 0
        ) {
          setError(t('login.poll_interval_invalid'));
          return false;
        }
      }
      setError('');
      return true;
    },
    [adminEmail, adminPassword, apiBase, cpaManagementKey, pollIntervalMs, t]
  );

  const handleUsageSetupNext = useCallback(() => {
    if (!validateUsageSetupStep(usageSetupStep)) return;
    const currentIndex = usageSetupSteps.indexOf(usageSetupStep);
    const nextStep = usageSetupSteps[Math.min(currentIndex + 1, usageSetupSteps.length - 1)];
    setUsageSetupStep(nextStep);
  }, [usageSetupStep, usageSetupSteps, validateUsageSetupStep]);

  const handleUsageSetupBack = useCallback(() => {
    setError('');
    const currentIndex = usageSetupSteps.indexOf(usageSetupStep);
    const previousStep = usageSetupSteps[Math.max(currentIndex - 1, 0)];
    setUsageSetupStep(previousStep);
  }, [usageSetupStep, usageSetupSteps]);

  const handleSubmit = useCallback(async () => {
    if (usageServiceNeedsSetup && !usageSetupIsLastStep) {
      handleUsageSetupNext();
      return;
    }

    const trimmedAdminEmail = adminEmail.trim();
    const trimmedCPAKey = cpaManagementKey.trim();
    const baseToUse = apiBase ? normalizeApiBase(apiBase) : detectedBase;

    if (usageServiceNeedsSetup) {
      if (!trimmedAdminEmail) {
        setError(t('login.email_required'));
        return;
      }
      if (!adminPassword) {
        setError(t('login.password_required'));
        return;
      }
      if (!apiBase.trim()) {
        setError(t('login.cpa_address_required'));
        return;
      }
      if (!trimmedCPAKey) {
        setError(t('login.cpa_management_key_required'));
        return;
      }
    } else if (isManagerServerMode) {
      if (!trimmedAdminEmail) {
        setError(t('login.email_required'));
        return;
      }
      if (!adminPassword) {
        setError(t('login.password_required'));
        return;
      }
    } else if (!trimmedCPAKey) {
      setError(t('login.cpa_management_key_required'));
      return;
    }

    const parsedPollIntervalMs = Number(pollIntervalMs);
    if (
      usageServiceNeedsSetup &&
      requestMonitoringEnabled &&
      (!/^\d+$/.test(pollIntervalMs.trim()) ||
        !Number.isFinite(parsedPollIntervalMs) ||
        parsedPollIntervalMs <= 0)
    ) {
      setError(t('login.poll_interval_invalid'));
      return;
    }

    setLoading(true);
    setError('');
    try {
      if (usageServiceNeedsSetup) {
        // Setup is protected by the same HttpOnly session as the rest of the
        // Manager UI, so authenticate before sending any deployment settings.
        await loginServiceApi.login(detectedBase, {
          email: trimmedAdminEmail,
          password: adminPassword,
          remember: rememberCredential,
        });
        await loginServiceApi.setup(detectedBase, {
          cpaBaseUrl: baseToUse,
          cpaManagementKey: trimmedCPAKey,
          pollIntervalMs: requestMonitoringEnabled ? parsedPollIntervalMs : undefined,
          ensureUsageStatisticsEnabled: requestMonitoringEnabled,
          requestMonitoringEnabled,
        });
        localStorage.setItem(USAGE_SERVICE_LAST_CPA_BASE_KEY, baseToUse);
      }

      const authStore = await loadAuthStore();
      const loginResult = await authStore.getState().login({
        apiBase: isManagerServerMode ? detectedBase : baseToUse,
        managementKey: isManagerServerMode ? undefined : trimmedCPAKey,
        email: isManagerServerMode ? trimmedAdminEmail : undefined,
        password: isManagerServerMode ? adminPassword : undefined,
        rememberPassword: rememberCredential,
        sessionMode: isManagerServerMode ? 'manager_embedded' : 'external_panel',
        sessionPanelBase: detectedBase,
        managerSessionEstablished: usageServiceNeedsSetup,
      });
      setAdminPassword('');
      if (loginResult.recoveryMode === 'manager_config') {
        localStorage.setItem(CONFIG_TAB_STORAGE_KEY, 'manager');
        navigateFromLogin('/config');
      } else {
        navigateFromLogin('/dashboard');
      }
    } catch (err: unknown) {
      const message = getLocalizedErrorMessage(err, t);
      setError(message);
    } finally {
      setLoading(false);
    }
  }, [
    adminEmail,
    adminPassword,
    apiBase,
    cpaManagementKey,
    detectedBase,
    handleUsageSetupNext,
    isManagerServerMode,
    pollIntervalMs,
    rememberCredential,
    requestMonitoringEnabled,
    t,
    usageServiceNeedsSetup,
    usageSetupIsLastStep,
  ]);

  const handleSubmitKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'Enter' && !loading) {
        event.preventDefault();
        handleSubmit();
      }
    },
    [handleSubmit, loading]
  );

  const handleCardPointerMove = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    const card = event.currentTarget;
    const bounds = card.getBoundingClientRect();
    const x = ((event.clientX - bounds.left) / bounds.width) * 100;
    const y = ((event.clientY - bounds.top) / bounds.height) * 100;
    card.style.setProperty('--pointer-x', `${Math.max(0, Math.min(100, x))}%`);
    card.style.setProperty('--pointer-y', `${Math.max(0, Math.min(100, y))}%`);
    card.style.setProperty('--card-rotate-x', `${((50 - y) * 0.07).toFixed(2)}deg`);
    card.style.setProperty('--card-rotate-y', `${((x - 50) * 0.07).toFixed(2)}deg`);
  }, []);

  const handleCardPointerLeave = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    const card = event.currentTarget;
    card.style.setProperty('--pointer-x', '50%');
    card.style.setProperty('--pointer-y', '0%');
    card.style.setProperty('--card-rotate-x', '0deg');
    card.style.setProperty('--card-rotate-y', '0deg');
  }, []);

  const showSplash = autoLoginSuccess;

  const renderSecretToggle = (
    visible: boolean,
    toggle: () => void,
    kind: 'key' | 'password' = 'key'
  ) => {
    const label = visible
      ? t(kind === 'password' ? 'login.hide_password' : 'login.hide_key')
      : t(kind === 'password' ? 'login.show_password' : 'login.show_key');
    return (
      <button
        type="button"
        className="btn btn-ghost btn-xs btn-icon-only"
        onClick={toggle}
        aria-label={label}
        title={label}
      >
        {visible ? <IconEyeOff size={16} /> : <IconEye size={16} />}
      </button>
    );
  };

  return (
    <div className={styles.container}>
      <div className={styles.toolBar}>
        <button
          type="button"
          className={styles.toolButton}
          onClick={cycleTheme}
          aria-label={t('theme.switch')}
          title={t('theme.switch')}
        >
          {theme === 'dark' ? <IconMoon size={17} /> : <IconSun size={17} />}
        </button>
        <div className={styles.languageMenu} ref={languageMenuRef}>
          <button
            type="button"
            className={styles.toolButton}
            onClick={toggleLanguageMenu}
            aria-label={t('language.switch')}
            title={t('language.switch')}
            aria-haspopup="menu"
            aria-expanded={languageMenuOpen}
          >
            <IconLanguages size={17} />
          </button>
          {languageMenuOpen && (
            <div className={styles.languagePopover} role="menu" aria-label={t('language.switch')}>
              {LANGUAGE_ORDER.map((lang) => (
                <button
                  key={lang}
                  type="button"
                  className={`${styles.languageOption} ${
                    language === lang ? styles.languageOptionActive : ''
                  }`}
                  onClick={() => handleLanguageSelect(lang)}
                  role="menuitemradio"
                  aria-checked={language === lang}
                >
                  {t(LANGUAGE_LABEL_KEYS[lang])}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className={styles.techDecor} aria-hidden="true">
        <span className={`${styles.techOrb} ${styles.techOrbPrimary}`} />
        <span className={`${styles.techOrb} ${styles.techOrbSecondary}`} />
        <span className={`${styles.techOrb} ${styles.techOrbTertiary}`} />
        <span className={styles.techCrosshair} />
        <span className={`${styles.orbitRing} ${styles.orbitRingPrimary}`} />
        <span className={`${styles.orbitRing} ${styles.orbitRingSecondary}`} />
        <span className={`${styles.dataStream} ${styles.dataStreamA}`} />
        <span className={`${styles.dataStream} ${styles.dataStreamB}`} />
        <span className={`${styles.dataStream} ${styles.dataStreamC}`} />
        <span className={`${styles.techParticle} ${styles.techParticleA}`} />
        <span className={`${styles.techParticle} ${styles.techParticleB}`} />
        <span className={`${styles.techParticle} ${styles.techParticleC}`} />
        <span className={`${styles.techParticle} ${styles.techParticleD}`} />
      </div>

      <div className={styles.formPanel}>
        {showSplash ? (
          <div className={styles.splashContent} role="status" aria-label={t('common.loading')}>
            <div className={styles.splashLoader}>
              <div className={styles.splashLoaderBar} />
            </div>
          </div>
        ) : (
          <div
            className={`${styles.formContent} ${
              usageServiceNeedsSetup ? styles.setupFormContent : ''
            }`}
          >
            <div
              className={`${styles.loginCard} ${usageServiceNeedsSetup ? styles.setupCard : ''}`}
              onPointerMove={handleCardPointerMove}
              onPointerLeave={handleCardPointerLeave}
            >
              <div className={styles.cardBranding}>
                <h1>{usageServiceNeedsSetup ? t('login.setup_title') : t('common.login')}</h1>
              </div>

              {usageServiceNeedsSetup && (
                <div className={styles.setupFlow}>
                  <div className={styles.stepper} aria-label={t('login.setup_steps')}>
                    {usageSetupSteps.map((step, index) => {
                      const isActive = index === usageSetupStepIndex;
                      const isDone = index < usageSetupStepIndex;
                      return (
                        <div
                          key={step}
                          className={`${styles.stepItem} ${isActive ? styles.stepItemActive : ''} ${
                            isDone ? styles.stepItemDone : ''
                          }`}
                          aria-current={isActive ? 'step' : undefined}
                        >
                          <span className={styles.stepIndex}>
                            {isDone ? <IconCheck size={18} /> : index + 1}
                          </span>
                          <span className={styles.stepLabel}>{usageSetupStepLabels[step]}</span>
                        </div>
                      );
                    })}
                  </div>

                  <div className={styles.stepPanel}>
                    <div className={styles.stepHeader}>
                      <span className={styles.stepEyebrow}>
                        {t('login.step_count', {
                          current: usageSetupStepIndex + 1,
                          total: usageSetupSteps.length,
                        })}
                      </span>
                      <h2>{usageSetupStepLabels[usageSetupStep]}</h2>
                    </div>

                    {usageSetupStep === 'admin' && (
                      <div className={styles.stepFields}>
                        <Input
                          autoFocus
                          label={t('login.email_label')}
                          placeholder={t('login.email_placeholder')}
                          type="email"
                          autoComplete="username"
                          value={adminEmail}
                          onChange={(event) => setAdminEmail(event.target.value)}
                          onKeyDown={handleSubmitKeyDown}
                          hint={t('login.admin_account_hint')}
                        />
                        <Input
                          label={t('login.password_label')}
                          placeholder={t('login.password_placeholder')}
                          type={showAdminPassword ? 'text' : 'password'}
                          autoComplete="current-password"
                          value={adminPassword}
                          onChange={(event) => setAdminPassword(event.target.value)}
                          onKeyDown={handleSubmitKeyDown}
                          rightElement={renderSecretToggle(
                            showAdminPassword,
                            () => setShowAdminPassword((prev) => !prev),
                            'password'
                          )}
                        />
                      </div>
                    )}

                    {usageSetupStep === 'connection' && (
                      <div className={styles.stepFields}>
                        <Input
                          autoFocus
                          label={t('login.cpa_connection_label')}
                          placeholder={t('login.cpa_connection_placeholder')}
                          value={apiBase}
                          onChange={(event) => setApiBase(event.target.value)}
                          onKeyDown={handleSubmitKeyDown}
                          hint={t('login.cpa_connection_hint')}
                        />
                      </div>
                    )}

                    {usageSetupStep === 'cpaKey' && (
                      <div className={styles.stepFields}>
                        <Input
                          autoFocus
                          label={t('login.cpa_management_key_label')}
                          placeholder={t('login.cpa_management_key_placeholder')}
                          type={showCPAManagementKey ? 'text' : 'password'}
                          value={cpaManagementKey}
                          onChange={(event) => setCPAManagementKey(event.target.value)}
                          onKeyDown={handleSubmitKeyDown}
                          hint={t('login.cpa_management_key_hint')}
                          rightElement={renderSecretToggle(showCPAManagementKey, () =>
                            setShowCPAManagementKey((prev) => !prev)
                          )}
                        />
                      </div>
                    )}

                    {usageSetupStep === 'monitoring' && (
                      <div className={styles.stepFields}>
                        <div className={styles.optionBox}>
                          <SelectionCheckbox
                            checked={requestMonitoringEnabled}
                            onChange={setRequestMonitoringEnabled}
                            ariaLabel={t('login.request_monitoring_enabled')}
                            label={t('login.request_monitoring_enabled')}
                            labelClassName={styles.toggleLabel}
                          />
                          <p>
                            {requestMonitoringEnabled
                              ? t('login.request_monitoring_enabled_hint')
                              : t('login.request_monitoring_disabled_hint')}
                          </p>
                        </div>
                      </div>
                    )}

                    {usageSetupStep === 'polling' && (
                      <div className={styles.stepFields}>
                        <Input
                          autoFocus
                          label={t('login.poll_interval_label')}
                          type="number"
                          min="1"
                          placeholder="500"
                          value={pollIntervalMs}
                          onChange={(event) => setPollIntervalMs(event.target.value)}
                          onKeyDown={handleSubmitKeyDown}
                          hint={t('login.poll_interval_hint')}
                        />
                      </div>
                    )}

                    {usageSetupStep === 'review' && (
                      <div className={styles.stepFields}>
                        <div className={styles.optionBox}>
                          <SelectionCheckbox
                            checked={rememberCredential}
                            onChange={setRememberCredential}
                            ariaLabel={t('login.remember_session_label')}
                            label={t('login.remember_session_label')}
                            labelClassName={styles.toggleLabel}
                          />
                        </div>
                        <div className={styles.reviewGrid}>
                          <div>
                            <span className={styles.reviewIcon}>
                              <IconShield size={18} />
                            </span>
                            <span>{t('login.email_label')}</span>
                            <strong>{adminEmail || '-'}</strong>
                          </div>
                          <div>
                            <span className={styles.reviewIcon}>
                              <IconShield size={18} />
                            </span>
                            <span>{t('login.password_label')}</span>
                            <strong>{adminPassword ? '************' : '-'}</strong>
                          </div>
                          <div>
                            <span className={styles.reviewIcon}>
                              <IconKey size={18} />
                            </span>
                            <span>{t('login.remember_session_label')}</span>
                            <strong>
                              {rememberCredential ? t('common.enabled') : t('common.disabled')}
                            </strong>
                          </div>
                          <div>
                            <span className={styles.reviewIcon}>
                              <IconInfo size={18} />
                            </span>
                            <span>{t('login.cpa_connection_label')}</span>
                            <strong>{apiBase || '-'}</strong>
                          </div>
                          <div>
                            <span className={styles.reviewIcon}>
                              <IconKey size={18} />
                            </span>
                            <span>{t('login.cpa_management_key_label')}</span>
                            <strong>{cpaManagementKey ? '************' : '-'}</strong>
                          </div>
                          <div>
                            <span className={styles.reviewIcon}>
                              <IconEye size={18} />
                            </span>
                            <span>{t('login.request_monitoring_enabled')}</span>
                            <strong>
                              {requestMonitoringEnabled
                                ? t('common.enabled')
                                : t('common.disabled')}
                            </strong>
                          </div>
                          {requestMonitoringEnabled && (
                            <div>
                              <span className={styles.reviewIcon}>
                                <IconTimer size={18} />
                              </span>
                              <span>{t('login.poll_interval_label')}</span>
                              <strong>{pollIntervalMs}</strong>
                            </div>
                          )}
                        </div>
                      </div>
                    )}
                  </div>

                  {error && <div className={styles.errorBox}>{error}</div>}

                  <div className={styles.stepActions}>
                    <Button
                      variant="secondary"
                      className={styles.setupBackButton}
                      onClick={handleUsageSetupBack}
                      disabled={usageSetupIsFirstStep || loading}
                    >
                      {t('common.previous')}
                    </Button>
                    {usageSetupIsLastStep ? (
                      <Button
                        className={styles.setupNextButton}
                        onClick={handleSubmit}
                        loading={loading}
                      >
                        {loading ? t('login.initializing') : t('login.initialize_button')}
                      </Button>
                    ) : (
                      <Button
                        className={styles.setupNextButton}
                        onClick={handleUsageSetupNext}
                        disabled={loading}
                      >
                        {t('common.next')}
                      </Button>
                    )}
                  </div>
                </div>
              )}

              {!usageServiceNeedsSetup && (
                <div className={styles.loginForm}>
                  {!isManagerServerMode && (
                    <>
                      <div className={styles.toggleAdvanced}>
                        <SelectionCheckbox
                          checked={showCustomBase}
                          onChange={setShowCustomBase}
                          ariaLabel={t('login.custom_connection_label')}
                          label={t('login.custom_connection_label')}
                          labelClassName={styles.toggleLabel}
                        />
                      </div>

                      {showCustomBase && (
                        <Input
                          label={t('login.custom_connection_label')}
                          placeholder={t('login.custom_connection_placeholder')}
                          value={apiBase}
                          onChange={(event) => setApiBase(event.target.value)}
                          hint={t('login.custom_connection_hint')}
                        />
                      )}
                    </>
                  )}

                  {isManagerServerMode ? (
                    <>
                      <Input
                        autoFocus
                        label={t('login.email_label')}
                        placeholder={t('login.email_placeholder')}
                        type="email"
                        autoComplete="username"
                        value={adminEmail}
                        onChange={(event) => setAdminEmail(event.target.value)}
                        onKeyDown={handleSubmitKeyDown}
                        hint={t('login.admin_account_hint')}
                      />
                      <Input
                        label={t('login.password_label')}
                        placeholder={t('login.password_placeholder')}
                        type={showAdminPassword ? 'text' : 'password'}
                        autoComplete="current-password"
                        value={adminPassword}
                        onChange={(event) => setAdminPassword(event.target.value)}
                        onKeyDown={handleSubmitKeyDown}
                        rightElement={renderSecretToggle(
                          showAdminPassword,
                          () => setShowAdminPassword((prev) => !prev),
                          'password'
                        )}
                      />
                    </>
                  ) : (
                    <Input
                      autoFocus
                      label={t('login.management_key_label')}
                      placeholder={t('login.management_key_placeholder')}
                      type={showCPAManagementKey ? 'text' : 'password'}
                      value={cpaManagementKey}
                      onChange={(event) => setCPAManagementKey(event.target.value)}
                      onKeyDown={handleSubmitKeyDown}
                      rightElement={renderSecretToggle(showCPAManagementKey, () =>
                        setShowCPAManagementKey((prev) => !prev)
                      )}
                    />
                  )}

                  <div className={styles.toggleAdvanced}>
                    <SelectionCheckbox
                      checked={rememberCredential}
                      onChange={setRememberCredential}
                      ariaLabel={t(
                        isManagerServerMode
                          ? 'login.remember_session_label'
                          : 'login.remember_credential_label'
                      )}
                      label={t(
                        isManagerServerMode
                          ? 'login.remember_session_label'
                          : 'login.remember_credential_label'
                      )}
                      labelClassName={styles.toggleLabel}
                    />
                  </div>

                  <Button fullWidth onClick={handleSubmit} loading={loading}>
                    {loading ? t('login.submitting') : t('login.submit_button')}
                  </Button>

                  {error && <div className={styles.errorBox}>{error}</div>}
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
