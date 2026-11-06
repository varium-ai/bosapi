import React, { useEffect, useId, useMemo, useRef, useState } from 'react';
import { Link, NavLink, useLocation, useParams } from 'react-router-dom';
import { copyText } from '../common/copyText';
import {
  ArrowLeftOutlined,
  ArrowRightOutlined,
  BookOutlined,
  BranchesOutlined,
  CheckOutlined,
  CodeOutlined,
  CopyOutlined,
  ExportOutlined,
  RocketOutlined,
  BulbOutlined,
  ApiOutlined,
  SlidersOutlined,
  RobotOutlined,
  InfoCircleOutlined,
  KeyOutlined,
  MenuOutlined,
  QuestionCircleOutlined,
  SearchOutlined,
  SafetyOutlined,
  ThunderboltOutlined,
  TeamOutlined,
  AppstoreOutlined,
  CloseOutlined,
  RightOutlined,
  BarChartOutlined,
  DashboardOutlined,
} from '@ant-design/icons';
import { docPages as englishPages } from './content';
import { agentBrands } from './brands';
import { useI18n, LocaleSettingsControl } from '../../i18n';
import { DEFAULT_CONNECTION, fillSnippet, searchDocs } from './helpers';
import { useGatewayConfig } from '../../config/gateway';
import type { DocBlock, DocConnection, DocPage } from './types';
import './docs.css';
import { configurationErrorDoc } from '../diagnostics/documentation';

const icons: Record<string, React.ReactNode> = {
  spark: <RocketOutlined />,
  code: <ApiOutlined />,
  terminal: <CodeOutlined />,
  concepts: <BulbOutlined />,
  provider: <ApiOutlined />,
  models: <SlidersOutlined />,
  agents: <RobotOutlined />,
  layers: <BranchesOutlined />,
  grid: <AppstoreOutlined />,
  users: <TeamOutlined />,
  key: <KeyOutlined />,
  route: <BranchesOutlined />,
  chart: <BarChartOutlined />,
  shield: <SafetyOutlined />,
  bolt: <ThunderboltOutlined />,
  help: <QuestionCircleOutlined />,
  ...Object.fromEntries(
    Object.entries(agentBrands).map(([key, brands]) => [
      key,
      <span
        key={key}
        className={`docs-agent-brands${brands.length > 1 ? ' docs-agent-brands-pair' : ''}`}
        aria-hidden="true"
      >
        {brands.map(brand => (
          <img
            key={brand.file}
            className={`docs-brand-icon docs-brand-icon-${key}`}
            src={`/docs/brands/${brand.file}`}
            alt=""
            title={brand.name}
            draggable={false}
          />
        ))}
      </span>,
    ]),
  ),
};

function Inline({ text, connection }: { text: string; connection: DocConnection }) {
  return (
    <>
      {fillSnippet(text, connection)
        .split(/(`[^`]+`|\[[^\]]+\]\((?:https:\/\/|\/docs\/)[^\s)]+\))/g)
        .map((part, index) => {
          if (part.startsWith('`')) return <code key={index}>{part.slice(1, -1)}</code>;
          const link = part.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
          if (!link) return part;
          return link[2].startsWith('/docs/') ? (
            <Link key={index} to={link[2]}>
              {link[1]}
            </Link>
          ) : (
            <a key={index} href={link[2]} target="_blank" rel="noopener noreferrer">
              {link[1]}
            </a>
          );
        })}
    </>
  );
}

function Highlight({ text }: { text: string }) {
  const tokens = text.split(
    /("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|#[^\n]*|\/\/[^\n]*|\b(?:import|from|export|const|new|await|for|in|if|true|false|True|False|print)\b)/g,
  );
  return (
    <>
      {tokens.map((token, i) => (
        <span
          key={i}
          className={
            /^["']/.test(token)
              ? 'docs-token-string'
              : /^(#|\/\/)/.test(token)
                ? 'docs-token-comment'
                : /^(import|from|export|const|new|await|for|in|if|true|false|True|False|print)$/.test(
                      token,
                    )
                  ? 'docs-token-keyword'
                  : undefined
          }
        >
          {token}
        </span>
      ))}
    </>
  );
}

function CopyButton({ value }: { value: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<'idle' | 'copied' | 'error'>('idle');
  useEffect(() => {
    setState('idle');
  }, [value]);
  useEffect(() => {
    if (state === 'idle') return;
    const timer = window.setTimeout(() => setState('idle'), 2500);
    return () => window.clearTimeout(timer);
  }, [state]);
  return (
    <button
      type="button"
      className="docs-copy"
      aria-label={t(state === 'copied' ? 'docs.copy_success' : 'docs.copy_code')}
      onClick={async () => {
        setState((await copyText(value)) ? 'copied' : 'error');
      }}
    >
      {state === 'copied' ? <CheckOutlined /> : <CopyOutlined />}
      <span aria-live="polite">
        {t(
          state === 'copied' ? 'docs.copied' : state === 'error' ? 'docs.copy_error' : 'docs.copy',
        )}
      </span>
    </button>
  );
}

function CodeBlock({
  block,
  connection,
}: {
  block: Extract<DocBlock, { type: 'code' | 'tabs' }>;
  connection: DocConnection;
}) {
  const { t } = useI18n();
  const [active, setActive] = useState(0);
  const id = useId();
  const tabs = block.type === 'tabs' ? block.tabs : [block];
  const selected = tabs[active] || tabs[0];
  const value = fillSnippet(selected.code, connection);
  return (
    <div className="docs-code-block">
      <div className="docs-code-header">
        {block.type === 'tabs' ? (
          <div role="tablist" aria-label={t('docs.code_language')} className="docs-code-tabs">
            {tabs.map((tab, i) => (
              <button
                key={tab.label}
                type="button"
                role="tab"
                id={`${id}-tab-${i}`}
                aria-controls={`${id}-panel`}
                aria-selected={active === i}
                tabIndex={active === i ? 0 : -1}
                onClick={() => setActive(i)}
                onKeyDown={event => {
                  if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
                    event.preventDefault();
                    const next =
                      event.key === 'Home'
                        ? 0
                        : event.key === 'End'
                          ? tabs.length - 1
                          : (i + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
                    setActive(next);
                    document.getElementById(`${id}-tab-${next}`)?.focus();
                  }
                }}
              >
                {tab.label}
              </button>
            ))}
          </div>
        ) : (
          <span className="docs-code-label">{selected.label}</span>
        )}
        <CopyButton value={value} />
      </div>
      <div
        id={`${id}-panel`}
        role={block.type === 'tabs' ? 'tabpanel' : undefined}
        aria-labelledby={block.type === 'tabs' ? `${id}-tab-${active}` : undefined}
      >
        <pre tabIndex={0} aria-label={`${selected.label} ${t('docs.example')}`}>
          <code>
            <Highlight text={value} />
          </code>
        </pre>
      </div>
    </div>
  );
}

export default function DocsPage() {
  return <Documentation docPages={englishPages} />;
}

function Documentation({ docPages: basePages }: { docPages: DocPage[] }) {
  const { language, t } = useI18n();
  const docPages = useMemo(
    () => [...basePages, configurationErrorDoc('en-US')],
    [basePages, language],
  );
  const docGroups = useMemo(() => [...new Set(docPages.map(item => item.group))], [docPages]);
  const { slug = 'quickstart' } = useParams();
  const location = useLocation();
  const page = docPages.find(item => item.slug === slug);
  const gateway = useGatewayConfig();
  const connection = useMemo<DocConnection>(
    () => ({ ...DEFAULT_CONNECTION, origin: gateway.endpoint }),
    [gateway.endpoint],
  );
  const [query, setQuery] = useState('');
  const [searchOpen, setSearchOpen] = useState(false);
  const [resultIndex, setResultIndex] = useState(0);
  const [mobileNav, setMobileNav] = useState(false);
  const [activeSection, setActiveSection] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);
  const results = useMemo(() => searchDocs(docPages, query), [docPages, query]);
  const searchVisible = searchOpen && !!query.trim();
  const selectedResult = Math.min(resultIndex, Math.max(0, results.length - 1));

  useEffect(() => {
    if (searchVisible)
      resultsRef.current
        ?.querySelector<HTMLElement>('[aria-selected="true"]')
        ?.scrollIntoView({ block: 'nearest' });
  }, [searchVisible, selectedResult, results]);

  useEffect(() => {
    document.title = `${page?.title || t('docs.not_found')} | BOS API ${t('docs.navigation')}`;
  }, [page, t]);

  useEffect(() => {
    setMobileNav(false);
    setSearchOpen(false);
    setQuery('');
    setResultIndex(0);
    setActiveSection(page?.sections[0]?.id || '');
    const frame = requestAnimationFrame(() => {
      let target: HTMLElement | null = null;
      try {
        target = location.hash
          ? document.getElementById(decodeURIComponent(location.hash.slice(1)))
          : null;
      } catch {
        /* Invalid fragments fall back to the title. */
      }
      if (target) {
        target.scrollIntoView();
        target.focus({ preventScroll: true });
        setActiveSection(target.id);
      } else {
        window.scrollTo({ top: 0, behavior: 'instant' as ScrollBehavior });
        headingRef.current?.focus({ preventScroll: true });
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [slug, location.hash]);

  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        searchRef.current?.focus();
        setSearchOpen(true);
      }
      if (event.key === 'Escape') {
        setSearchOpen(false);
        setMobileNav(false);
      }
    };
    window.addEventListener('keydown', shortcut);
    return () => window.removeEventListener('keydown', shortcut);
  }, []);

  useEffect(() => {
    const observer = new IntersectionObserver(
      entries => {
        const visible = entries
          .filter(entry => entry.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]) setActiveSection(visible[0].target.id);
      },
      { rootMargin: '-95px 0px -55% 0px' },
    );
    page?.sections.forEach(item => {
      const node = document.getElementById(item.id);
      if (node) observer.observe(node);
    });
    return () => observer.disconnect();
  }, [page]);

  const renderBlock = (block: DocBlock, index: number) => {
    const text = (value: string) => <Inline text={value} connection={connection} />;
    switch (block.type) {
      case 'paragraph':
        return <p key={index}>{text(block.text)}</p>;
      case 'note':
        return (
          <aside
            key={index}
            className={`docs-note ${block.tone === 'warning' ? 'docs-note-warning' : ''}`}
          >
            <InfoCircleOutlined />
            <div>
              <strong>{block.title}</strong>
              <p>{text(block.text)}</p>
            </div>
          </aside>
        );
      case 'list': {
        const Tag = block.ordered ? 'ol' : 'ul';
        return (
          <Tag key={index} className={block.ordered ? 'docs-steps' : 'docs-list'}>
            {block.items.map((value, i) => (
              <li key={i}>{text(value)}</li>
            ))}
          </Tag>
        );
      }
      case 'table':
        return (
          <div key={index} className="docs-table-wrap">
            <table>
              <thead>
                <tr>
                  {block.headers.map((value, i) => (
                    <th key={i}>{value}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {block.rows.map((row, i) => (
                  <tr key={i}>
                    {row.map((value, j) => (
                      <td key={j}>{text(value)}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      case 'code':
      case 'tabs':
        return <CodeBlock key={index} block={block} connection={connection} />;
      case 'cards':
        return (
          <div key={index} className="docs-cards">
            {block.items.map(item => (
              <Link key={item.href} to={item.href} className="docs-card">
                <span className="docs-card-icon">
                  {icons[
                    docPages.find(page => `/docs/${page.slug}` === item.href)?.icon ||
                      item.icon ||
                      'code'
                  ] || <BookOutlined />}
                </span>
                <strong>
                  {item.title}
                  <ArrowRightOutlined />
                </strong>
                <span>{item.text}</span>
              </Link>
            ))}
          </div>
        );
    }
  };

  const pageIndex = docPages.findIndex(item => item.slug === slug);
  const previous = docPages[pageIndex - 1];
  const next = docPages[pageIndex + 1];
  const nav = (
    <>
      {docGroups.map(group => (
        <div className="docs-nav-group" key={group}>
          <h2>{group}</h2>
          {docPages
            .filter(item => item.group === group)
            .map(item => (
              <NavLink
                to={`/docs/${item.slug}`}
                key={item.slug}
                className={({ isActive }) => `docs-nav-link${isActive ? ' is-active' : ''}`}
              >
                <span>{icons[item.icon]}</span>
                {item.slug === 'quickstart' ? t('docs.quickstart') : item.title}
              </NavLink>
            ))}
        </div>
      ))}
    </>
  );

  return (
    <div className="docs-site">
      <a className="docs-skip" href="#docs-content">
        {t('docs.skip')}
      </a>
      <header className="docs-header">
        <div className="docs-header-inner">
          <Link to="/docs/quickstart" className="docs-brand">
            <img src="/logo.svg" alt="" />
            <strong>BOS API</strong>
            <span className="docs-brand-divider" />
            <span>{t('docs.navigation')}</span>
          </Link>
          <nav className="docs-top-nav" aria-label={t('docs.sections')}>
            <Link
              className={
                page?.group === docGroups[0] || page?.group === docGroups[1] ? 'is-active' : ''
              }
              to="/docs/quickstart"
            >
              {t('docs.guides')}
            </Link>
            <Link
              className={page?.group === docGroups[2] ? 'is-active' : ''}
              to="/docs/api-integration"
            >
              {t('docs.api_reference')}
            </Link>
            <Link
              className={page?.group === docGroups[3] ? 'is-active' : ''}
              to="/docs/coding-agents"
            >
              {t('docs.agents')}
            </Link>
          </nav>
          <div
            className="docs-search"
            onBlur={event => {
              if (!event.currentTarget.contains(event.relatedTarget as Node)) setSearchOpen(false);
            }}
          >
            <SearchOutlined />
            <input
              ref={searchRef}
              role="combobox"
              aria-autocomplete="list"
              aria-haspopup="listbox"
              placeholder={t('docs.search_placeholder')}
              aria-label={t('docs.search')}
              aria-expanded={searchVisible}
              aria-controls={searchVisible ? 'docs-search-results' : undefined}
              aria-activedescendant={
                searchVisible && results.length ? `docs-search-result-${selectedResult}` : undefined
              }
              value={query}
              onFocus={() => setSearchOpen(true)}
              onChange={event => {
                setQuery(event.target.value);
                setResultIndex(0);
                setSearchOpen(true);
              }}
              onKeyDown={event => {
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                  event.preventDefault();
                  const count = Math.max(results.length, 1);
                  setResultIndex(
                    searchVisible
                      ? (selectedResult + (event.key === 'ArrowDown' ? 1 : -1) + count) % count
                      : event.key === 'ArrowDown'
                        ? 0
                        : count - 1,
                  );
                  setSearchOpen(true);
                }
                if (event.key === 'Enter' && searchVisible) {
                  event.preventDefault();
                  resultsRef.current
                    ?.querySelectorAll<HTMLAnchorElement>('a')
                    [selectedResult]?.click();
                }
              }}
            />
            <kbd>Ctrl K</kbd>
            {searchVisible && (
              <div
                id="docs-search-results"
                role="listbox"
                aria-label={t('docs.search')}
                className="docs-search-results"
                ref={resultsRef}
              >
                <div className="docs-search-caption" role="status">
                  {results.length
                    ? t('docs.matches').replace('{count}', String(results.length))
                    : t('docs.no_matches')}
                </div>
                {results.map(({ page: result, section: match }, i) => (
                  <Link
                    id={`docs-search-result-${i}`}
                    role="option"
                    aria-selected={i === selectedResult}
                    tabIndex={-1}
                    className={i === selectedResult ? 'is-selected' : ''}
                    key={result.slug}
                    to={`/docs/${result.slug}${match ? `#${match.id}` : ''}`}
                    onClick={() => {
                      setSearchOpen(false);
                      setQuery('');
                    }}
                  >
                    <span>{icons[result.icon]}</span>
                    <div>
                      <strong>{result.title}</strong>
                      <small>{match?.title || result.group}</small>
                    </div>
                    <RightOutlined />
                  </Link>
                ))}
                {!results.length && <p>{t('docs.search_hint')}</p>}
              </div>
            )}
          </div>
          <div className="docs-locale">
            <LocaleSettingsControl languageOnly />
          </div>
          <Link className="docs-console-link" to="/">
            <DashboardOutlined aria-hidden="true" />
            {t('docs.console')}
          </Link>
          <button
            type="button"
            className="docs-menu-toggle"
            aria-label={t(mobileNav ? 'docs.close_navigation' : 'docs.open_navigation')}
            aria-expanded={mobileNav}
            aria-controls="docs-mobile-nav"
            onClick={() => setMobileNav(value => !value)}
          >
            {mobileNav ? <CloseOutlined /> : <MenuOutlined />}
          </button>
        </div>
      </header>
      {mobileNav && (
        <nav
          id="docs-mobile-nav"
          className="docs-mobile-nav"
          aria-label={t('docs.mobile_navigation')}
        >
          {nav}
        </nav>
      )}
      <div className="docs-layout">
        <aside className="docs-sidebar">
          <nav aria-label={t('docs.navigation')}>{nav}</nav>
        </aside>
        <main id="docs-content" className="docs-main" tabIndex={-1}>
          {page ? (
            <>
              <div className="docs-article-header">
                <h1 ref={headingRef} tabIndex={-1}>
                  {page.title}
                </h1>
                <p className="docs-lede">{page.description}</p>
              </div>
              {!gateway.endpoint && (
                <aside className="docs-note docs-note-warning" role="status">
                  <InfoCircleOutlined />
                  <div>
                    <p>{t('gateway_settings.not_configured')}</p>
                  </div>
                </aside>
              )}
              <details className="docs-mobile-toc">
                <summary>{t('docs.toc')}</summary>
                {page.sections.map(item => (
                  <Link key={item.id} to={`#${item.id}`}>
                    {item.title}
                  </Link>
                ))}
              </details>
              <article key={page.slug} lang={language}>
                {page.sections.map(item => (
                  <section id={item.id} className="docs-section" key={item.id} tabIndex={-1}>
                    <h2>
                      <Link
                        to={`#${item.id}`}
                        aria-label={t('docs.link_to').replace('{title}', item.title)}
                      >
                        {item.title}
                        <span aria-hidden="true">#</span>
                      </Link>
                    </h2>
                    {item.blocks.map(renderBlock)}
                  </section>
                ))}
              </article>
              {!!page.sources?.length && (
                <section className="docs-sources">
                  <h2>{t('docs.sources')}</h2>
                  <p>{t('docs.sources_note')}</p>
                  {page.sources.map(source => (
                    <a key={source.url} href={source.url} target="_blank" rel="noreferrer">
                      {source.title}
                      <ExportOutlined />
                    </a>
                  ))}
                </section>
              )}
              <nav className="docs-pagination" aria-label={t('docs.pagination')}>
                {previous ? (
                  <Link to={`/docs/${previous.slug}`}>
                    <small>
                      <ArrowLeftOutlined /> {t('docs.previous')}
                    </small>
                    <strong>{previous.title}</strong>
                  </Link>
                ) : (
                  <div />
                )}
                {next && (
                  <Link className="docs-next" to={`/docs/${next.slug}`}>
                    <small>
                      {t('docs.next')} <ArrowRightOutlined />
                    </small>
                    <strong>{next.title}</strong>
                  </Link>
                )}
              </nav>
            </>
          ) : (
            <div className="docs-not-found">
              <span className="docs-eyebrow">404 / {t('docs.navigation')}</span>
              <h1 ref={headingRef} tabIndex={-1}>
                {t('docs.not_found')}
              </h1>
              <p>{t('docs.not_found_hint')}</p>
              <Link className="docs-button" to="/docs/quickstart">
                {t('docs.back')} <ArrowRightOutlined />
              </Link>
            </div>
          )}
        </main>
        <aside className="docs-toc" aria-label={t('docs.toc')}>
          {page && (
            <>
              <h2>{t('docs.toc')}</h2>
              <nav>
                {page.sections.map(item => (
                  <Link
                    className={activeSection === item.id ? 'is-active' : ''}
                    aria-current={activeSection === item.id ? 'location' : undefined}
                    to={`#${item.id}`}
                    key={item.id}
                  >
                    {item.title}
                  </Link>
                ))}
              </nav>
            </>
          )}
        </aside>
      </div>
    </div>
  );
}
