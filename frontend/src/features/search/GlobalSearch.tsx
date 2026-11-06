import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { Alert, Button, Input, Modal, Spin } from 'antd';
import type { InputRef } from 'antd';
import { SearchOutlined } from '@ant-design/icons';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useI18n } from '../../i18n';
import { positiveID } from '../onboarding/firstKey';
import { buildSearchIndex, searchEntries, type DocMetadata } from './searchIndex';
import './search.css';

export function GlobalSearch({ role, organizationID }: { role?: string; organizationID?: number }) {
  const { language, t } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const scope =
    role === 'super_admin'
      ? positiveID(params.get('setup_org')) || positiveID(params.get('organization_id'))
      : organizationID;
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState(0);
  const [docs, setDocs] = useState<DocMetadata[]>([]);
  const [status, setStatus] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle');
  const input = useRef<InputRef>(null);
  const listID = useId();
  const zh = language === 'zh-CN';
  const load = () => {
    setStatus('loading');
    void import('./docsMetadata.json')
      .then(data => {
        setDocs(data.default as DocMetadata[]);
        setStatus('ready');
      })
      .catch(() => setStatus('error'));
  };
  useEffect(() => {
    if (open && status === 'idle') load();
  }, [open, status]);
  // Context changes synchronously rebuild the index and clear the active selection.
  const context = `${role}:${scope}:${language}`;
  useEffect(() => {
    setQuery('');
    setSelected(0);
  }, [context]);
  useEffect(() => {
    setOpen(false);
  }, [location.key]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        event.altKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== 'k'
      )
        return;
      // Configuration forms already own modal focus and their discard confirmation.
      if (!open && document.querySelector('.ant-modal-wrap:not([style*="display: none"])')) return;
      event.preventDefault();
      setOpen(value => !value);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);
  const index = useMemo(
    () => buildSearchIndex(role, language, t, docs, scope),
    [role, language, t, docs, scope],
  );
  const results = useMemo(() => {
    const matches = searchEntries(index, query);
    return ['page', 'action', 'help'].flatMap(kind => matches.filter(entry => entry.kind === kind));
  }, [index, query]);
  const active = Math.min(selected, Math.max(0, results.length - 1));
  useEffect(() => {
    if (open) document.getElementById(`${listID}-${active}`)?.scrollIntoView({ block: 'nearest' });
  }, [active, open, listID]);
  const choose = (href: string) => {
    setOpen(false);
    navigate(href);
  };
  const group = (kind: string) =>
    kind === 'page'
      ? zh
        ? '功能页面'
        : 'Pages'
      : kind === 'action'
        ? zh
          ? '操作入口'
          : 'Actions'
        : zh
          ? '帮助文档'
          : 'Help';
  return (
    <>
      <Button
        className="console-search-trigger"
        icon={<SearchOutlined />}
        onClick={() => {
          setQuery('');
          setSelected(0);
          setOpen(true);
        }}
        aria-label={zh ? '全局搜索，Control 或 Command K' : 'Global search, Control or Command K'}
      >
        <span className="console-search-label">
          {zh ? '搜索功能、文档、错误码' : 'Search pages, help, error codes'}
        </span>
        <kbd>⌘ / Ctrl K</kbd>
      </Button>
      <Modal
        open={open}
        onCancel={() => setOpen(false)}
        footer={null}
        title={zh ? '全局搜索' : 'Global search'}
        width={680}
        afterOpenChange={visible => {
          if (visible) input.current?.focus();
        }}
        destroyOnHidden
      >
        <Input
          ref={input}
          value={query}
          maxLength={200}
          onChange={event => {
            setQuery(event.target.value);
            setSelected(0);
          }}
          placeholder={
            zh
              ? '例如：上游、创建 Key、超时、provider_auth_failed'
              : 'Try: upstream, create Key, timeout, provider_auth_failed'
          }
          role="combobox"
          aria-autocomplete="list"
          aria-expanded={open}
          aria-controls={listID}
          aria-activedescendant={results.length ? `${listID}-${active}` : undefined}
          aria-label={zh ? '搜索关键词' : 'Search terms'}
          onKeyDown={event => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
              event.preventDefault();
              setSelected(
                (active + (event.key === 'ArrowDown' ? 1 : -1) + results.length) %
                  (results.length || 1),
              );
            }
            if (event.key === 'Enter' && results[active]) {
              event.preventDefault();
              choose(results[active].href);
            }
          }}
        />
        {status === 'loading' && <Spin size="small" />}
        {status === 'error' && (
          <Alert
            type="warning"
            message={
              zh
                ? '文档索引加载失败，页面搜索仍可用。'
                : 'Help index failed to load. Page search is available.'
            }
            action={<Button onClick={load}>{zh ? '重试' : 'Retry'}</Button>}
          />
        )}
        {!query && (
          <p className="console-search-hint">
            {zh
              ? '常用入口 · ↑ ↓ 选择 · Enter 打开 · Esc 关闭'
              : 'Common destinations · ↑ ↓ select · Enter open · Esc close'}
          </p>
        )}
        <div
          id={listID}
          role="listbox"
          aria-label={zh ? '搜索结果' : 'Search results'}
          className="console-search-results"
        >
          {results.map((entry, i) => (
            <div key={entry.id}>
              {(i === 0 || results[i - 1].kind !== entry.kind) && (
                <div role="presentation" className="console-search-group">
                  {group(entry.kind)}
                </div>
              )}
              <div
                id={`${listID}-${i}`}
                role="option"
                aria-selected={active === i}
                className={`console-search-result ${active === i ? 'is-active' : ''}`}
                onMouseEnter={() => setSelected(i)}
                onMouseDown={event => event.preventDefault()}
                onClick={() => choose(entry.href)}
              >
                <strong>{entry.name}</strong>
                <span>{entry.module}</span>
                <p>{entry.description}</p>
              </div>
            </div>
          ))}
        </div>
        {results.length === 0 && (
          <p>
            {zh
              ? '未找到结果，试试更短的关键词或同义词。'
              : 'No results. Try a shorter keyword or synonym.'}{' '}
            <a href="/docs/quickstart" target="_blank" rel="noreferrer">
              {zh ? '打开帮助' : 'Open help'}
            </a>
          </p>
        )}
      </Modal>
    </>
  );
}
