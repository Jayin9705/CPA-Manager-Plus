import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select, type SelectOption } from '@/components/ui/Select';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import {
  apiKeysApi,
  playgroundApi,
  type PlaygroundChatMessage,
  type PlaygroundImageResult,
} from '@/services/api';
import { usageServiceApi } from '@/services/api/usageService';
import { useAuthStore, useConfigStore } from '@/stores';
import type { ModelInfo } from '@/utils/models';
import styles from './PlaygroundPage.module.scss';

const DEFAULT_CHAT_MODEL = 'gpt-5.4-mini';
const DEFAULT_IMAGE_MODEL = 'gpt-image-2';

type PlaygroundMode = 'chat' | 'image';

interface ChatMessage extends PlaygroundChatMessage {
  id: string;
}

const normalizeAPIKeys = (input: unknown): string[] => {
  if (!Array.isArray(input)) return [];
  const seen = new Set<string>();
  return input.reduce<string[]>((keys, value) => {
    const key = typeof value === 'string' ? value.trim() : '';
    if (!key || seen.has(key)) return keys;
    seen.add(key);
    keys.push(key);
    return keys;
  }, []);
};

const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  return 'Request failed';
};

const createMessageId = (): string => `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;

const getImageSource = (image: PlaygroundImageResult): string => {
  if (image.b64Json) {
    return `data:${image.mimeType || 'image/png'};base64,${image.b64Json}`;
  }
  return image.url || '';
};

const getModelLabel = (model: ModelInfo): string =>
  model.alias && model.alias !== model.name ? `${model.name} (${model.alias})` : model.name;

export function PlaygroundPage() {
  const { t } = useTranslation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const availability = usePanelFeatureAvailability();
  const managerPlaygroundBaseUrl =
    availability.panelHostMode === 'manager_embedded' ? availability.managerServiceBase : undefined;

  const [mode, setMode] = useState<PlaygroundMode>('chat');
  const [targetBaseUrl, setTargetBaseUrl] = useState('');
  const [targetLoading, setTargetLoading] = useState(true);
  const [fallbackApiKey, setFallbackApiKey] = useState('');
  const [apiKeyLoading, setApiKeyLoading] = useState(true);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [chatModel, setChatModel] = useState(DEFAULT_CHAT_MODEL);
  const [imageModel, setImageModel] = useState(DEFAULT_IMAGE_MODEL);
  const [chatMessages, setChatMessages] = useState<ChatMessage[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [chatError, setChatError] = useState('');
  const [imagePrompt, setImagePrompt] = useState('');
  const [imageSize, setImageSize] = useState('1024x1024');
  const [imageQuality, setImageQuality] = useState('auto');
  const [generatedImages, setGeneratedImages] = useState<PlaygroundImageResult[]>([]);
  const [imageLoading, setImageLoading] = useState(false);
  const [imageError, setImageError] = useState('');
  const chatEndRef = useRef<HTMLDivElement | null>(null);

  const apiKey = normalizeAPIKeys(config?.apiKeys)[0] || fallbackApiKey;

  useEffect(() => {
    if (config || connectionStatus !== 'connected') return;
    void fetchConfig().catch(() => undefined);
  }, [config, connectionStatus, fetchConfig]);

  useEffect(() => {
    let cancelled = false;

    if (connectionStatus !== 'connected') {
      setFallbackApiKey('');
      setApiKeyLoading(false);
      return () => {
        cancelled = true;
      };
    }

    const configuredKey = normalizeAPIKeys(config?.apiKeys)[0];
    if (configuredKey) {
      setFallbackApiKey('');
      setApiKeyLoading(false);
      return () => {
        cancelled = true;
      };
    }

    setApiKeyLoading(true);
    void apiKeysApi
      .list()
      .then((keys) => {
        if (!cancelled) setFallbackApiKey(normalizeAPIKeys(keys)[0] || '');
      })
      .catch(() => {
        if (!cancelled) setFallbackApiKey('');
      })
      .finally(() => {
        if (!cancelled) setApiKeyLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [config?.apiKeys, connectionStatus]);

  useEffect(() => {
    let cancelled = false;

    const resolveTarget = async () => {
      setTargetLoading(true);

      if (availability.checking) return;

      if (availability.panelHostMode === 'manager_embedded') {
        if (!availability.managerServiceBase) {
          if (!cancelled) {
            setTargetBaseUrl('');
            setTargetLoading(false);
          }
          return;
        }

        try {
          const response = await usageServiceApi.getManagerConfig(
            availability.managerServiceBase,
            managementKey || undefined
          );
          if (!cancelled) {
            const cpaBaseUrl = response.config.cpaConnection?.cpaBaseUrl || '';
            setTargetBaseUrl(cpaBaseUrl);
            setTargetLoading(false);
          }
        } catch {
          if (!cancelled) {
            setTargetBaseUrl('');
            setTargetLoading(false);
          }
        }
        return;
      }

      if (!cancelled) {
        setTargetBaseUrl(apiBase);
        setTargetLoading(false);
      }
    };

    void resolveTarget();
    return () => {
      cancelled = true;
    };
  }, [
    apiBase,
    availability.checking,
    availability.managerServiceBase,
    availability.panelHostMode,
    managementKey,
  ]);

  const refreshModels = useCallback(async () => {
    if (!targetBaseUrl.trim()) return;

    try {
      const result = await playgroundApi.fetchModels(targetBaseUrl, apiKey);
      setModels(result);
      setChatModel((current) => current || result[0]?.name || DEFAULT_CHAT_MODEL);
    } catch {
      setModels([]);
    }
  }, [apiKey, targetBaseUrl]);

  useEffect(() => {
    if (targetLoading || apiKeyLoading || !targetBaseUrl.trim() || availability.checking) return;
    void refreshModels();
  }, [apiKeyLoading, availability.checking, refreshModels, targetBaseUrl, targetLoading]);

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [chatLoading, chatMessages]);

  const modelOptions = useMemo(
    () => models.map((model) => ({ value: model.name, label: getModelLabel(model) })),
    [models]
  );
  const imageSizeOptions: SelectOption[] = useMemo(
    () => [
      { value: '1024x1024', label: '1024 × 1024' },
      { value: '1536x1024', label: '1536 × 1024' },
      { value: '1024x1536', label: '1024 × 1536' },
    ],
    []
  );
  const imageQualityOptions: SelectOption[] = useMemo(
    () => [
      { value: 'auto', label: t('playground.quality_auto') },
      { value: 'low', label: t('playground.quality_low') },
      { value: 'medium', label: t('playground.quality_medium') },
      { value: 'high', label: t('playground.quality_high') },
    ],
    [t]
  );

  const validateRequest = (
    model: string,
    prompt: string,
    setError: (message: string) => void
  ): boolean => {
    if (!targetBaseUrl.trim()) {
      setError(t('playground.target_unavailable'));
      return false;
    }
    if (apiKeyLoading || !apiKey.trim()) {
      setError(t('playground.api_key_required'));
      return false;
    }
    if (!model.trim()) {
      setError(t('playground.model_required'));
      return false;
    }
    if (!prompt.trim()) {
      setError(t('playground.prompt_required'));
      return false;
    }
    return true;
  };

  const handleChatSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const prompt = chatInput.trim();
    setChatError('');
    if (!validateRequest(chatModel, prompt, setChatError)) return;

    const userMessage: ChatMessage = { id: createMessageId(), role: 'user', content: prompt };
    const requestMessages: PlaygroundChatMessage[] = [
      ...chatMessages.map(({ role, content }) => ({ role, content })),
      { role: 'user', content: prompt },
    ];
    setChatMessages((current) => [...current, userMessage]);
    setChatInput('');
    setChatLoading(true);

    try {
      const response = await playgroundApi.chat(
        targetBaseUrl,
        apiKey,
        chatModel,
        requestMessages,
        managerPlaygroundBaseUrl
      );
      setChatMessages((current) => [
        ...current,
        {
          id: createMessageId(),
          role: 'assistant',
          content: response.text || t('playground.empty_response'),
        },
      ]);
    } catch (error) {
      setChatError(getErrorMessage(error));
    } finally {
      setChatLoading(false);
    }
  };

  const handleImageSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const prompt = imagePrompt.trim();
    setImageError('');
    if (!validateRequest(imageModel, prompt, setImageError)) return;

    setImageLoading(true);
    setGeneratedImages([]);
    try {
      const images = await playgroundApi.generateImages(
        targetBaseUrl,
        apiKey,
        {
          model: imageModel,
          prompt,
          size: imageSize,
          quality: imageQuality === 'auto' ? undefined : imageQuality,
        },
        managerPlaygroundBaseUrl
      );
      setGeneratedImages(images);
      if (!images.length) setImageError(t('playground.no_images'));
    } catch (error) {
      setImageError(getErrorMessage(error));
    } finally {
      setImageLoading(false);
    }
  };

  return (
    <div className={styles.page}>
      <section className={styles.workspacePanel}>
        <div className={styles.modeTabs} role="tablist" aria-label={t('playground.mode_label')}>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'chat'}
            className={`${styles.modeTab} ${mode === 'chat' ? styles.modeTabActive : ''}`}
            onClick={() => setMode('chat')}
          >
            <span className={styles.modeTabIcon} aria-hidden="true">
              ⌁
            </span>
            {t('playground.chat')}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'image'}
            className={`${styles.modeTab} ${mode === 'image' ? styles.modeTabActive : ''}`}
            onClick={() => setMode('image')}
          >
            <span className={styles.modeTabIcon} aria-hidden="true">
              ✦
            </span>
            {t('playground.image')}
          </button>
        </div>

        {mode === 'chat' ? (
          <div className={styles.workspaceContent}>
            <div className={styles.toolbarRow}>
              <div className={styles.modelField}>
                <Input
                  label={t('playground.model')}
                  value={chatModel}
                  onChange={(event) => setChatModel(event.target.value)}
                  placeholder={t('playground.model_placeholder')}
                  list="playground-chat-models"
                />
                <datalist id="playground-chat-models">
                  {modelOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </datalist>
              </div>
            </div>

            <div className={styles.chatPanel} aria-live="polite">
              {chatMessages.length ? (
                chatMessages.map((message) => (
                  <div
                    className={`${styles.messageRow} ${message.role === 'user' ? styles.messageRowUser : ''}`}
                    key={message.id}
                  >
                    <div className={styles.messageAvatar} aria-hidden="true">
                      {message.role === 'user' ? 'U' : 'AI'}
                    </div>
                    <div className={styles.messageBody}>
                      <div className={styles.messageRole}>
                        {message.role === 'user' ? t('playground.you') : t('playground.assistant')}
                      </div>
                      <div className={styles.messageText}>{message.content}</div>
                    </div>
                  </div>
                ))
              ) : (
                <div className={styles.emptyState}>
                  <div className={styles.emptyIcon} aria-hidden="true">
                    ✦
                  </div>
                  <strong>{t('playground.chat_empty_title')}</strong>
                  <span>{t('playground.chat_empty_description')}</span>
                </div>
              )}
              {chatLoading && (
                <div className={styles.messageRow}>
                  <div className={styles.messageAvatar} aria-hidden="true">
                    AI
                  </div>
                  <div className={styles.messageBody}>
                    <div className={styles.messageRole}>{t('playground.assistant')}</div>
                    <div className={styles.loadingDots} aria-label={t('playground.thinking')}>
                      <span />
                      <span />
                      <span />
                    </div>
                  </div>
                </div>
              )}
              <div ref={chatEndRef} />
            </div>

            {chatError && <div className={styles.errorBox}>{chatError}</div>}

            <form className={styles.composer} onSubmit={handleChatSubmit}>
              <textarea
                className={styles.composerInput}
                value={chatInput}
                onChange={(event) => setChatInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault();
                    event.currentTarget.form?.requestSubmit();
                  }
                }}
                placeholder={t('playground.chat_placeholder')}
                rows={3}
                disabled={chatLoading}
              />
              <div className={styles.composerFooter}>
                <span className={styles.subtleText}>{t('playground.chat_shortcut')}</span>
                <Button type="submit" loading={chatLoading} disabled={!chatInput.trim()}>
                  {t('playground.send')}
                </Button>
              </div>
            </form>
          </div>
        ) : (
          <div className={styles.workspaceContent}>
            <form className={styles.imageForm} onSubmit={handleImageSubmit}>
              <div className={styles.toolbarRow}>
                <div className={styles.modelField}>
                  <Input
                    label={t('playground.model')}
                    value={imageModel}
                    onChange={(event) => setImageModel(event.target.value)}
                    placeholder={t('playground.image_model_placeholder')}
                    list="playground-image-models"
                  />
                  <datalist id="playground-image-models">
                    <option value={DEFAULT_IMAGE_MODEL} />
                    <option value="gpt-image-1.5" />
                    <option value="grok-imagine-image" />
                    {modelOptions.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </datalist>
                </div>
                <div className={styles.compactField}>
                  <label htmlFor="playground-image-size">{t('playground.image_size')}</label>
                  <Select
                    id="playground-image-size"
                    value={imageSize}
                    onChange={setImageSize}
                    options={imageSizeOptions}
                    ariaLabel={t('playground.image_size')}
                  />
                </div>
                <div className={styles.compactField}>
                  <label htmlFor="playground-image-quality">{t('playground.image_quality')}</label>
                  <Select
                    id="playground-image-quality"
                    value={imageQuality}
                    onChange={setImageQuality}
                    options={imageQualityOptions}
                    ariaLabel={t('playground.image_quality')}
                  />
                </div>
              </div>
              <div className={styles.promptField}>
                <label htmlFor="playground-image-prompt">{t('playground.image_prompt')}</label>
                <textarea
                  id="playground-image-prompt"
                  className={styles.promptInput}
                  value={imagePrompt}
                  onChange={(event) => setImagePrompt(event.target.value)}
                  placeholder={t('playground.image_prompt_placeholder')}
                  rows={5}
                  disabled={imageLoading}
                />
              </div>
              {imageError && <div className={styles.errorBox}>{imageError}</div>}
              <div className={styles.imageFormFooter}>
                <span className={styles.subtleText}>{t('playground.image_hint')}</span>
                <Button type="submit" loading={imageLoading} disabled={!imagePrompt.trim()}>
                  {t('playground.generate')}
                </Button>
              </div>
            </form>

            <div className={styles.imageResults}>
              {generatedImages.length ? (
                generatedImages.map((image, index) => {
                  const source = getImageSource(image);
                  return (
                    <figure className={styles.imageCard} key={`${source}-${index}`}>
                      <img src={source} alt={image.revisedPrompt || imagePrompt} loading="lazy" />
                      <figcaption>
                        <span>{image.revisedPrompt || t('playground.image_generated')}</span>
                        <a href={source} download={`cpa-playground-${index + 1}.png`}>
                          {t('playground.download')}
                        </a>
                      </figcaption>
                    </figure>
                  );
                })
              ) : (
                <div className={styles.imageEmptyState}>
                  <div className={styles.emptyIcon} aria-hidden="true">
                    ✦
                  </div>
                  <strong>{t('playground.image_empty_title')}</strong>
                  <span>{t('playground.image_empty_description')}</span>
                </div>
              )}
            </div>
          </div>
        )}
      </section>
    </div>
  );
}
